//go:build windows

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// removeSelf deletes the running executable: Windows will not delete a running
// program, so a detached cmd waits for this process to exit and deletes it.
func removeSelf(path string) error {
	old := path + ".old"
	os.Remove(old)
	if err := os.Rename(path, old); err != nil {
		return err
	}
	cmd := exec.Command("cmd.exe", "/c", "ping -n 3 127.0.0.1 >nul & del /f /q \""+old+"\"")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008} // DETACHED_PROCESS
	return cmd.Start()
}

// removeFromPath takes the install directory off the user's PATH, which install.ps1 added.
func removeFromPath(dir string) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	value, valueType, err := key.GetStringValue("Path")
	if err != nil {
		return
	}
	var kept []string
	changed := false
	for _, part := range strings.Split(value, ";") {
		if part != "" && strings.EqualFold(filepath.Clean(part), filepath.Clean(dir)) {
			changed = true
			continue
		}
		kept = append(kept, part)
	}
	if !changed {
		return
	}
	joined := strings.Join(kept, ";")
	if valueType == registry.EXPAND_SZ {
		_ = key.SetExpandStringValue("Path", joined)
	} else {
		_ = key.SetStringValue("Path", joined)
	}
}
