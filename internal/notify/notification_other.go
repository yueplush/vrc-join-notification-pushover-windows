//go:build !windows

package notify

import "errors"

func sendToast(title, message string) error {
	return errors.New("desktop notifications require Windows")
}
