package audio

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"time"

	clioutput "github.com/openai/openai-cli/internal/output"
)

// Each explicit Save creates a private, unique directory. Repeated recordings
// never overwrite existing exports, including paths replaced with symlinks.
func saveTerminalTranslation(ctx context.Context, result terminalTranslationResult, parent string) (saved string, err error) {
	defer func() {
		if err != nil {
			err = audioFailure("Could not save translation. Check the folder permissions and free space; the recording remains available.", err)
		}
	}()
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if result.stage == nil {
		return "", errors.New("no retained translation exists")
	}
	parent, err = audioExpandSaveHome(parent)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	directory, err := terminalTranslationDirectory(parent, "translation-"+time.Now().Format("20060102-150405")+"-")
	if err != nil {
		return "", err
	}
	identity, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		_ = os.Remove(directory)
		return "", err
	}
	owned := map[string]os.FileInfo{}
	defer func() {
		if err != nil {
			for name, identity := range owned {
				if current, statErr := root.Lstat(name); statErr == nil && os.SameFile(current, identity) {
					err = errors.Join(err, root.Remove(name))
				}
			}
		}
		err = errors.Join(err, root.Close())
		if err != nil {
			if current, statErr := os.Lstat(directory); statErr == nil && os.SameFile(current, identity) {
				err = errors.Join(err, os.Remove(directory))
			}
			saved = ""
		}
	}()
	write := func(name string, emit func(*os.File) error) error {
		f, createErr := clioutput.CreatePrivateDownloadFile(root, name)
		if createErr != nil {
			return createErr
		}
		info, statErr := f.Stat()
		if statErr != nil {
			return errors.Join(statErr, f.Close())
		}
		owned[name] = info
		writeErr := emit(f)
		if writeErr == nil {
			writeErr = f.Sync()
		}
		return errors.Join(writeErr, f.Close())
	}
	copyFile := func(destination, source string, header []byte) error {
		input, err := result.stage.reader(source)
		if err != nil {
			return err
		}
		if len(header) > 0 && input.Size() != result.AudioBytes {
			return errors.New("the retained audio size changed")
		}
		return write(destination, func(out *os.File) error {
			if len(header) > 0 {
				if _, err := out.Write(header); err != nil {
					return err
				}
			}
			return terminalTranslationCopy(ctx, out, input)
		})
	}
	if result.AudioBytes > 0 {
		header, headerErr := terminalTranslationWAVHeader(result.AudioBytes, result.SampleRate, result.Channels)
		if headerErr != nil {
			return "", headerErr
		}
		if err = copyFile("translation.wav", "translation.pcm", header); err != nil {
			return "", err
		}
	}
	for _, artifact := range []struct{ name, path string }{{"original.txt", result.OriginalPath}, {"translation.txt", result.TranslationPath}} {
		if artifact.path == "" {
			continue
		}
		if err = copyFile(artifact.name, artifact.name, nil); err != nil {
			return "", err
		}
	}
	err = write("session.json", func(file *os.File) error {
		return json.NewEncoder(file).Encode(struct {
			Complete   bool  `json:"complete"`
			SampleRate int   `json:"sample_rate"`
			Channels   int   `json:"channels"`
			AudioBytes int64 `json:"audio_bytes"`
		}{result.Complete, result.SampleRate, result.Channels, result.AudioBytes})
	})
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	return filepath.Clean(directory), nil
}
func terminalTranslationWAVHeader(size int64, rate, channels int) ([]byte, error) {
	if rate <= 0 || channels < 1 || channels > 65535/2 || uint64(rate) > uint64(math.MaxUint32)/uint64(channels*2) || size < 0 || size%int64(channels*2) != 0 {
		return nil, errors.New("the recording has invalid PCM audio settings")
	}
	if size > math.MaxUint32-36 {
		return nil, errors.New("the recording is too large for a WAV file")
	}
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(size+36))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(header[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(size))
	return header, nil
}
