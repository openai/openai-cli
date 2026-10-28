package autocomplete

import (
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func pickerTestSecurityLabel(names, value string, listErr, readErr error) func() (pickerSecurityLabel, error) {
	return func() (pickerSecurityLabel, error) {
		return readPickerSecurityLabel(func(data []byte) (int, error) {
			copy(data, names)
			return len(names), listErr
		}, func(data []byte) (int, error) {
			copy(data, value)
			return len(value), readErr
		})
	}
}

func TestPickerSecurityLabelsPreserveReplacementContract(t *testing.T) {
	const labelName = "security.selinux\x00"
	unreadable := errors.New("cannot read attribute")
	for _, test := range []struct {
		name, beforeNames, before, afterNames, after string
		readErr, listErr                             error
		allowed                                      bool
	}{
		{name: "unlabeled", allowed: true},
		{name: "matching inherited label", beforeNames: labelName, before: "user_u:object_r:user_home_t:s0\x00", afterNames: labelName, after: "user_u:object_r:user_home_t:s0\x00", allowed: true},
		{name: "empty labels still present", beforeNames: labelName, afterNames: labelName, allowed: true},
		{name: "empty is not absent", beforeNames: labelName},
		{name: "label lost", beforeNames: labelName, before: "original"},
		{name: "label added", afterNames: labelName, after: "added"},
		{name: "different label", beforeNames: labelName, before: "original", afterNames: labelName, after: "different"},
		{name: "read failure", beforeNames: labelName, readErr: unreadable},
		{name: "list failure", listErr: unreadable},
		{name: "user metadata", beforeNames: "user.note\x00"},
		{name: "ACL", beforeNames: "system.posix_acl_access\x00"},
		{name: "capabilities", beforeNames: "security.capability\x00"},
		{name: "label and other metadata", beforeNames: labelName + "user.note\x00"},
		{name: "metadata on staging", afterNames: "user.note\x00"},
		{name: "unterminated name", beforeNames: "security.selinux"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := comparePickerSecurityLabels(
				pickerTestSecurityLabel(test.beforeNames, test.before, test.listErr, test.readErr),
				pickerTestSecurityLabel(test.afterNames, test.after, nil, nil),
			)
			if test.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPickerSecurityLabelChangesAreRefused(t *testing.T) {
	for _, size := range []int{-1, 1, 65537} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			calls := 0
			_, err := readPickerSecurityLabel(func(data []byte) (int, error) {
				return copy(data, "security.selinux\x00"), nil
			}, func(data []byte) (int, error) {
				calls++
				if calls == 1 {
					return size, nil
				}
				return 0, errors.New("attribute changed before reading")
			})
			require.Error(t, err)
		})
	}
	// Recheck the opened metadata for every validation; a changed profile label
	// must not be accepted just because an earlier staging check matched.
	profile := pickerTestSecurityLabel("security.selinux\x00", "original", nil, nil)
	staged := pickerTestSecurityLabel("security.selinux\x00", "original", nil, nil)
	require.NoError(t, comparePickerSecurityLabels(profile, staged))
	profile = pickerTestSecurityLabel("security.selinux\x00", "changed", nil, nil)
	require.Error(t, comparePickerSecurityLabels(profile, staged))
}
