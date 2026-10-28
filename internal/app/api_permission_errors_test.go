package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
)

const missingFilesPermission = "You have insufficient permissions for this operation. Missing scopes: api.files.write. Check that you have the correct role."

func TestAPIPermissionErrorRecognizesOnlySupportedDiagnostics(t *testing.T) {
	const prefix = "You have insufficient permissions for this operation. Missing scopes: "
	for _, test := range []struct {
		name    string
		message string
		want    bool
	}{
		{"canonical", missingFilesPermission, true},
		{"scope only", prefix + "api.files.write", true},
		{"trailing period", prefix + "api.files.write.", true},
		{"multiple scopes", prefix + "api.batch.write, api.files.write, api.files.read. Check access.", true},
		{"private suffix", missingFilesPermission + " synthetic-private-prompt token=synthetic-secret", true},
		{"unknown", prefix + "api.unknown.write.", false},
		{"substring", prefix + "api.files.write_extra.", false},
		{"extra segment", prefix + "api.files.write.extra.", false},
		{"quoted diagnosis", "The prompt contains: " + missingFilesPermission, false},
		{"loose prose", "Missing scopes: api.files.write", false},
		{"invalid list", prefix + "api.files.write, !!!.", false},
		{"empty scope", prefix + "api.files.write, .", false},
		{"newline", missingFilesPermission + "\n", false},
		{"terminal control", missingFilesPermission + "\x1b]52;c;synthetic-secret\a", false},
		{"direction control", missingFilesPermission + "\u202e", false},
	} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest, http.StatusInternalServerError} {
			t.Run(fmt.Sprintf("%s/%d", test.name, status), func(t *testing.T) {
				apierr := &openai.Error{StatusCode: status, Message: test.message}
				var out bytes.Buffer
				require.NoError(t, ShowCommandError(readableErrorTestCommand(t), apierr, &out))
				want := test.want && (status == http.StatusUnauthorized || status == http.StatusForbidden)
				require.Equal(t, want, strings.Contains(out.String(), "The API reports missing permission: api.files.write."))
				for _, private := range []string{"synthetic-", "token=", "\x1b", "\a", "\u202e"} {
					require.NotContains(t, out.String(), private)
				}
				if want {
					for _, step := range []string{"https://platform.openai.com/api-keys", "Files Write", "Batch Write", "project owner", "Correct access before retrying the failed operation."} {
						require.Contains(t, out.String(), step)
					}
				} else {
					// Authentication and generic access guidance have separate contracts.
					// Unsupported diagnostics must never imply a Files permission.
					require.NotContains(t, out.String(), "The API reports missing permission:")
					require.NotContains(t, out.String(), "Files Write")
					require.Contains(t, out.String(), fmt.Sprintf("HTTP %d", status))
				}
			})
		}
	}
}

func TestAPIPermissionErrorPreservesCauseAndCancellation(t *testing.T) {
	apierr := &openai.Error{StatusCode: http.StatusForbidden, Message: missingFilesPermission}
	root := readableErrorTestCommand(t)
	wrapped := fmt.Errorf("synthetic-private-wrapper: %w", apierr)
	for _, failure := range []error{apierr, wrapped, errors.Join(errors.New("synthetic-private-detail"), wrapped)} {
		var out bytes.Buffer
		require.NoError(t, ShowCommandError(root, failure, &out))
		require.Contains(t, out.String(), "The API reports missing permission: api.files.write.")
		require.NotContains(t, out.String(), "synthetic-private")
		require.ErrorIs(t, failure, apierr)
		var original *openai.Error
		require.ErrorAs(t, failure, &original)
		require.Same(t, apierr, original)
	}
	var canceled bytes.Buffer
	require.NoError(t, ShowCommandError(root, errors.Join(wrapped, context.Canceled), &canceled))
	require.Equal(t, "Request canceled.\n", canceled.String())

}

func TestAPIPermissionErrorPreservesWriterFailures(t *testing.T) {
	apierr := &openai.Error{StatusCode: http.StatusUnauthorized, Message: missingFilesPermission}
	root := readableErrorTestCommand(t)
	cause := errors.New("synthetic write failure")
	require.ErrorIs(t, ShowCommandError(root, apierr, errorSink{cause}), cause)
	require.ErrorIs(t, ShowCommandError(root, apierr, shortErrorSink{}), io.ErrShortWrite)
}
