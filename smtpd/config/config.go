package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	// Server settings
	Hostname    string `json:"hostname"`
	ListenAddr  string `json:"listen_addr"`
	MaxSize     int64  `json:"max_size"`      // Max message size in bytes
	MaxRecipients int  `json:"max_recipients"` // Max recipients per message

	// TLS settings
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`

	// Authentication
	RequireAuth bool   `json:"require_auth"`
	AuthFile    string `json:"auth_file"` // Path to user credentials file

	// Storage
	MailDir string `json:"mail_dir"` // Directory to store received emails
	QueueDir string `json:"queue_dir"` // Directory for outgoing mail queue

	// Relay settings for sending
	RelayHost     string `json:"relay_host"`     // External SMTP relay (optional)
	RelayPort     int    `json:"relay_port"`
	RelayUser     string `json:"relay_user"`
	RelayPassword string `json:"relay_password"`

	// Domain settings
	LocalDomains []string `json:"local_domains"` // Domains we accept mail for
}

var C Config

func Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return json.NewDecoder(f).Decode(&C)
}

func Default() Config {
	return Config{
		Hostname:      "localhost",
		ListenAddr:    ":25",
		MaxSize:       10 * 1024 * 1024, // 10MB
		MaxRecipients: 100,
		MailDir:       "./maildir",
		QueueDir:      "./queue",
		LocalDomains:  []string{"localhost"},
	}
}
