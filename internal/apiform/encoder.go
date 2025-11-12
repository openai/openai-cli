package apiform

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Marshal encodes a value as multipart form data using default settings
func Marshal(value any, writer *multipart.Writer) error {
	e := &encoder{
		format: FormatRepeat,
	}
	return e.marshal(value, writer)
}

// MarshalWithSettings encodes a value with custom array format
func MarshalWithSettings(value any, writer *multipart.Writer, arrayFormat FormFormat) error {
	e := &encoder{
		format: arrayFormat,
	}
	return e.marshal(value, writer)
}

type encoder struct {
	format FormFormat
}

func (e *encoder) marshal(value any, writer *multipart.Writer) error {
	val := reflect.ValueOf(value)
	if !val.IsValid() {
		return nil
	}
	return e.encodeValue("", val, writer)
}

func (e *encoder) encodeValue(key string, val reflect.Value, writer *multipart.Writer) error {
	if !val.IsValid() {
		return writer.WriteField(key, "")
	}

	t := val.Type()

	if t.Implements(reflect.TypeOf((*io.Reader)(nil)).Elem()) {
		return e.encodeReader(key, val, writer)
	}

	switch t.Kind() {
	case reflect.Pointer:
		if val.IsNil() || !val.IsValid() {
			return writer.WriteField(key, "")
		}
		return e.encodeValue(key, val.Elem(), writer)

	case reflect.Slice, reflect.Array:
		return e.encodeArray(key, val, writer)

	case reflect.Map:
		return e.encodeMap(key, val, writer)

	case reflect.Interface:
		if val.IsNil() {
			return writer.WriteField(key, "")
		}
		return e.encodeValue(key, val.Elem(), writer)

	case reflect.String:
		return writer.WriteField(key, val.String())

	case reflect.Bool:
		if val.Bool() {
			return writer.WriteField(key, "true")
		}
		return writer.WriteField(key, "false")

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return writer.WriteField(key, strconv.FormatInt(val.Int(), 10))

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return writer.WriteField(key, strconv.FormatUint(val.Uint(), 10))

	case reflect.Float32:
		return writer.WriteField(key, strconv.FormatFloat(val.Float(), 'f', -1, 32))

	case reflect.Float64:
		return writer.WriteField(key, strconv.FormatFloat(val.Float(), 'f', -1, 64))

	default:
		return fmt.Errorf("unknown type: %s", t.String())
	}
}

func (e *encoder) encodeArray(key string, val reflect.Value, writer *multipart.Writer) error {
	if e.format == FormatComma {
		var values []string
		for i := 0; i < val.Len(); i++ {
			item := val.Index(i)
			if (item.Kind() == reflect.Pointer || item.Kind() == reflect.Interface) && item.IsNil() {
				// Null values are sent as an empty string
				values = append(values, "")
				continue
			}
			// If item is an interface, reduce it to the concrete type
			if item.Kind() == reflect.Interface {
				item = item.Elem()
			}
			var strValue string
			switch item.Kind() {
			case reflect.String:
				strValue = item.String()
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				strValue = strconv.FormatInt(item.Int(), 10)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				strValue = strconv.FormatUint(item.Uint(), 10)
			case reflect.Float32, reflect.Float64:
				strValue = strconv.FormatFloat(item.Float(), 'f', -1, 64)
			case reflect.Bool:
				strValue = strconv.FormatBool(item.Bool())
			default:
				return fmt.Errorf("comma format not supported for complex array elements")
			}
			values = append(values, strValue)
		}
		return writer.WriteField(key, strings.Join(values, ","))
	}

	for i := 0; i < val.Len(); i++ {
		var formattedKey string
		switch e.format {
		case FormatRepeat:
			formattedKey = key
		case FormatBrackets:
			formattedKey = key + "[]"
		case FormatIndicesDots:
			if key == "" {
				formattedKey = strconv.Itoa(i)
			} else {
				formattedKey = key + "." + strconv.Itoa(i)
			}
		case FormatIndicesBrackets:
			if key == "" {
				formattedKey = strconv.Itoa(i)
			} else {
				formattedKey = key + "[" + strconv.Itoa(i) + "]"
			}
		default:
			return fmt.Errorf("apiform: unsupported array format")
		}

		if err := e.encodeValue(formattedKey, val.Index(i), writer); err != nil {
			return err
		}
	}
	return nil
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}

func (e *encoder) encodeReader(key string, val reflect.Value, writer *multipart.Writer) error {
	reader, ok := val.Convert(reflect.TypeOf((*io.Reader)(nil)).Elem()).Interface().(io.Reader)
	if !ok {
		return nil
	}

	// Set defaults
	filename := "anonymous_file"
	contentType := "application/octet-stream"

	// Get filename if available
	if named, ok := reader.(interface{ Filename() string }); ok {
		filename = named.Filename()
	} else if named, ok := reader.(interface{ Name() string }); ok {
		filename = path.Base(named.Name())
	}

	// Get content type if available
	if typed, ok := reader.(interface{ ContentType() string }); ok {
		contentType = typed.ContentType()
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
		escapeQuotes(key), escapeQuotes(filename)))
	h.Set("Content-Type", contentType)

	filewriter, err := writer.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(filewriter, reader)
	return err
}

func (e *encoder) encodeMap(key string, val reflect.Value, writer *multipart.Writer) error {
	type mapPair struct {
		key   string
		value reflect.Value
	}

	if key != "" {
		key = key + "."
	}

	// Collect and sort map entries for deterministic output
	pairs := []mapPair{}
	iter := val.MapRange()
	for iter.Next() {
		if iter.Key().Type().Kind() != reflect.String {
			return fmt.Errorf("cannot encode a map with a non string key")
		}
		pairs = append(pairs, mapPair{key: iter.Key().String(), value: iter.Value()})
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].key < pairs[j].key
	})

	// Process sorted pairs
	for _, p := range pairs {
		if err := e.encodeValue(key+p.key, p.value, writer); err != nil {
			return err
		}
	}

	return nil
}
