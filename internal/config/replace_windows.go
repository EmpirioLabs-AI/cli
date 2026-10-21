//go:build windows

package config

import (
	"os"
	"time"
)

// replace renames over an existing file. Windows refuses while another
// process (an editor, a virus scanner) holds the target open, so retry briefly.
func replace(from, to string) error {
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}
