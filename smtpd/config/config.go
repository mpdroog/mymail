package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	// Server settings
	Hostname      string `json:"hostname"`
	ListenAddr    string `json:"listen_addr"`
	MaxSizeStr    string `json:"max_size"`       // Human-readable size (e.g., "10MB")
	MaxSize       int64  `json:"-"`              // Parsed size in bytes
	MaxRecipients int    `json:"max_recipients"` // Max recipients per message

	// TLS settings
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`

	// Authentication
	AuthFile string `json:"auth_file"` // Path to user credentials file

	// Storage
	MailDir  string `json:"mail_dir"`  // Directory to store received emails
	QueueDir string `json:"queue_dir"` // Directory for outgoing mail queue

	// Relay settings for sending
	RelayHost     string `json:"relay_host"` // External SMTP relay (optional)
	RelayPort     int    `json:"relay_port"`
	RelayUser     string `json:"relay_user"`
	RelayPassword string `json:"relay_password"`

	// Domain settings
	LocalDomains []string `json:"local_domains"` // Domains we accept mail for

	// Sender whitelist
	EnableWhitelist bool     `json:"enable_whitelist"` // Enable sender whitelist
	WhitelistEmails []string `json:"whitelist_emails"` // Whitelisted email addresses

	RejectMsg string `json:"reject_msg"`
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

	// Parse human-readable size
	if C.MaxSizeStr != "" {
		size, err := parseSize(C.MaxSizeStr)
		if err != nil {
			return fmt.Errorf("invalid max_size %q: %v", C.MaxSizeStr, err)
		}
		C.MaxSize = size
	}

	return CheckPaths()
}

// parseSize converts human-readable size strings to bytes.
// Supports: B, KB, MB, GB (case-insensitive)
// Examples: "10MB", "512KB", "1GB", "1024"
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}

	re := regexp.MustCompile(`^(\d+)\s*(B|KB|MB|GB)?$`)
	matches := re.FindStringSubmatch(s)
	if matches == nil {
		return 0, fmt.Errorf("invalid format")
	}

	value, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, err
	}

	unit := matches[2]
	switch unit {
	case "GB":
		return value * 1024 * 1024 * 1024, nil
	case "MB":
		return value * 1024 * 1024, nil
	case "KB":
		return value * 1024, nil
	case "B", "":
		return value, nil
	default:
		return 0, fmt.Errorf("Invalid unit=" + unit)
	}

	return value, nil
}
