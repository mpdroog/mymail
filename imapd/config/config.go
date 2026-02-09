package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	// Server settings
	ListenAddr   string `json:"listen_addr"`
	InsecureAuth bool   `json:"insecure_auth"` // Allow auth without TLS

	// TLS settings
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`

	// Authentication
	AuthFile string `json:"auth_file"` // Path to user credentials file (username:password per line)

	// Storage
	MailDir string `json:"mail_dir"` // Directory with maildir structure
	Domain string `json:"domain"`
}

var (
	C       Config
	Verbose bool
)

func Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(&C); err != nil {
		return err
	}

	return CheckPaths()
}

func CheckPaths() error {
	if C.MailDir == "" {
		return fmt.Errorf("mail_dir not configured")
	}

	info, err := os.Stat(C.MailDir)
	if err != nil {
		return fmt.Errorf("mail_dir %q does not exist: %w", C.MailDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("mail_dir %q is not a directory", C.MailDir)
	}

	if C.AuthFile == "" {
		return fmt.Errorf("auth_file not configured")
	}

	return nil
}
