package updatenotice

import (
	"context"
	"errors"
	"os"
	"time"
)

const (
	// Released installers rewrite state.json and discard fields they do not recognize.
	updateModeName   = "update-mode.json"
	manualUpdates    = "manual"
	automaticUpdates = "automatic"
	migrationNotice  = "Updates are now manual. To enable automatic updates again, run openai updates enable."
)

// ErrUnmanagedInstallation lets the command select setup for the installation's owner.
var ErrUnmanagedInstallation = errors.New("automatic updates require a private OpenAI CLI installation")

func validateEnableEnvironment() error {
	if _, enabled := updatePolicy(os.Getenv); !enabled {
		return errors.New("check OPENAI_CLI_NO_AUTO_UPDATE, OPENAI_CLI_NO_UPDATE_CHECK, and OPENAI_CLI_VERSION_PIN before enabling automatic updates")
	}
	return nil
}

// SetAutomaticUpdates changes this installation's persisted preference.
func SetAutomaticUpdates(ctx context.Context, current string, enabled bool) error {
	mode := manualUpdates
	if enabled {
		if err := validateEnableEnvironment(); err != nil {
			return err
		}
		mode = automaticUpdates
	}
	err := configureAutomaticUpdates(ctx, current, mode)
	if !enabled && errors.Is(err, ErrUnmanagedInstallation) {
		return disablePortableWindowsUpdates(ctx, current)
	}
	return err
}

func configureAutomaticUpdates(ctx context.Context, current, mode string) error {
	if _, err := managedExecutablePath(false); err != nil {
		return ErrUnmanagedInstallation
	}
	installation, err := managedInstallation(current, false)
	if err != nil {
		return errors.New("cannot verify the private OpenAI CLI installation; use the installation instructions to repair it")
	}
	defer installation.close()
	ctx, cancel := context.WithTimeout(ctx, workerTimeout+10*time.Second)
	defer cancel()
	cache, err := openInstallerCache(ctx, true, nil, func() (*noticeCache, error) {
		return openManagedUpdateCache(ctx, installation)
	})
	if err != nil {
		return errors.New("cannot lock the update settings; check installation permissions and retry")
	}
	defer cache.close()
	state, err := loadUpdateState(cache, time.Now())
	if err != nil || !installation.unchanged() {
		return errors.New("cannot read the current installation's update settings; check the installation and retry")
	}
	if state.UpdateMode == mode {
		return nil
	}
	if mode == automaticUpdates {
		client := updateClient()
		defer client.CloseIdleConnections()
		delivery := releaseDelivery{client: client, verifier: systemUpdateVerifier(), privateVerifierEnabled: verifierEnrolled(installation.root.Name())}
		defer delivery.closeVerifier()
		if err := delivery.checkVerifier(ctx, cache); err != nil {
			return errors.New("cannot verify updates with GitHub CLI; install a compatible GitHub CLI and run openai updates enable again")
		}
	}
	if !installation.unchanged() || setUpdateMode(cache, &state, mode) != nil {
		return errors.New("could not save the update settings; check the installation and retry")
	}
	return nil
}

func setUpdateMode(cache *noticeCache, state *updateState, mode string) error {
	if !cache.modeRead {
		return errCache
	}
	if state.UpdateMode == mode {
		return nil
	}
	// A later opt-in must discover and authenticate a release from scratch.
	// Preserve applied and package identities needed by the existing installers.
	hadPending := state.Pending != nil
	state.Pending = nil
	state.Starting, state.Checked, state.ActivationChecked = 0, 0, 0
	state.VerifierUnavailable = false
	state.Announced = true
	if err := cache.saveState(state); err != nil {
		return err
	}
	if err := cache.saveRecord(updateModeName, &cache.mode, mode); err != nil {
		return err
	}
	state.UpdateMode = mode
	if hadPending {
		_ = removeUpdateFile(cache, "next")
	}
	return nil
}
