package client

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/mpdroog/mymail/smtpd/config"
)

type Client struct{}

func New() *Client {
	return &Client{}
}

// Send sends an email to the specified recipient
func (c *Client) Send(from, to string, data []byte) error {
	// If relay host is configured, use it
	if config.C.RelayHost != "" {
		return c.sendViaRelay(from, to, data)
	}

	// Otherwise, send directly via MX lookup
	return c.sendDirect(from, to, data)
}

func (c *Client) sendViaRelay(from, to string, data []byte) error {
	addr := fmt.Sprintf("%s:%d", config.C.RelayHost, config.C.RelayPort)

	var auth smtp.Auth
	if config.C.RelayUser != "" {
		auth = smtp.PlainAuth("", config.C.RelayUser, config.C.RelayPassword, config.C.RelayHost)
	}

	return smtp.SendMail(addr, auth, from, []string{to}, data)
}

func (c *Client) sendDirect(from, to string, data []byte) error {
	domain := getDomain(to)
	if domain == "" {
		return fmt.Errorf("invalid recipient address: %s", to)
	}

	// Look up MX records
	mxRecords, err := net.LookupMX(domain)
	if err != nil {
		return fmt.Errorf("MX lookup failed for %s: %v", domain, err)
	}

	if len(mxRecords) == 0 {
		// Fall back to A record
		mxRecords = []*net.MX{{Host: domain, Pref: 0}}
	}

	// Sort by preference
	sort.Slice(mxRecords, func(i, j int) bool {
		return mxRecords[i].Pref < mxRecords[j].Pref
	})

	var lastErr error
	for _, mx := range mxRecords {
		host := strings.TrimSuffix(mx.Host, ".")

		err := c.sendToHost(host, from, to, data)
		if err == nil {
			return nil
		}
		lastErr = err
	}

	return fmt.Errorf("all MX hosts failed, last error: %v", lastErr)
}

func (c *Client) sendToHost(host, from, to string, data []byte) error {
	// Try port 25 first
	conn, err := net.DialTimeout("tcp", host+":25", 30*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()

	// Say hello
	if err := client.Hello(config.C.Hostname); err != nil {
		return err
	}

	// Try STARTTLS if available
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{
			ServerName: host,
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			// Continue without TLS if STARTTLS fails
		}
	}

	// Set sender
	if err := client.Mail(from); err != nil {
		return err
	}

	// Set recipient
	if err := client.Rcpt(to); err != nil {
		return err
	}

	// Send data
	w, err := client.Data()
	if err != nil {
		return err
	}

	_, err = w.Write(data)
	if err != nil {
		return err
	}

	err = w.Close()
	if err != nil {
		return err
	}

	return client.Quit()
}

func getDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
