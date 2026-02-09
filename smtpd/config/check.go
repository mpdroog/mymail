package config

import (
	"fmt"
	"os"
	"path/filepath"
)

func checkWritable(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("directory does not exist: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}

	tmpFile := filepath.Join(dir, ".write-test")
	f, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("cannot create file: %w", err)
	}
	f.Close()
	os.Remove(tmpFile)

	return nil
}

func CheckPaths() error {
	if C.MailDir == "" {
		return fmt.Errorf("mail_dir not configured")
	}
	if err := checkWritable(C.MailDir); err != nil {
		return fmt.Errorf("mail_dir %q is not writable: %w", C.MailDir, err)
	}

	if C.QueueDir == "" {
		return fmt.Errorf("queue_dir not configured")
	}
	if err := checkWritable(C.QueueDir); err != nil {
		return fmt.Errorf("queue_dir %q is not writable: %w", C.QueueDir, err)
	}

	return nil
}