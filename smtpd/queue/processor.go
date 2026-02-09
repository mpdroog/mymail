package queue

import (
	"fmt"
	"log"
	"time"

	"github.com/mpdroog/mymail/smtpd/client"
	"github.com/mpdroog/mymail/smtpd/storage"
)

const (
	MaxRetries    = 5
	RetryInterval = 15 * time.Minute
)

type Processor struct {
	storage  *storage.Storage
	client   *client.Client
	quit     chan struct{}
	interval time.Duration
}

func NewProcessor(st *storage.Storage) *Processor {
	return &Processor{
		storage:  st,
		client:   client.New(),
		quit:     make(chan struct{}),
		interval: 1 * time.Minute,
	}
}

func (p *Processor) Start() {
	log.Println("Queue processor started")
	go p.run()
}

func (p *Processor) Stop() error {
	close(p.quit)
	log.Println("Queue processor stopped")
	return nil
}

func (p *Processor) run() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	// Process immediately on start
	p.processQueue()

	for {
		select {
		case <-ticker.C:
			e := p.processQueue()
			if e != nil {
				log.Printf("processQueue e=%v", e)
			}
		case <-p.quit:
			return
		}
	}
}

func (p *Processor) processQueue() error {
	emails, err := p.storage.GetQueuedEmails()
	if err != nil {
		return err
	}

	for _, email := range emails {
		if e := p.processEmail(&email); e != nil {
			log.Printf("processEmail e=%s", e.Error())
		}
	}

	return nil
}

func (p *Processor) processEmail(email *storage.QueuedEmail) error {
	log.Printf("Processing queued email %s to %s", email.ID, email.To)

	err := p.client.Send(email.From, email.To, email.Data)
	if err != nil {
		email.Attempts++
		email.LastError = err.Error()

		if email.Attempts >= MaxRetries {
			// Move to dead letter queue or notify sender
			p.handlePermanentFailure(email)
			return fmt.Errorf("Email %s failed permanently after %d attempts: %v", email.ID, email.Attempts, err)

		}

		// Schedule retry with exponential backoff
		backoff := time.Duration(email.Attempts) * RetryInterval
		email.NextRetry = time.Now().Add(backoff)

		log.Printf("Email %s failed (attempt %d), will retry at %v: %v",
			email.ID, email.Attempts, email.NextRetry, err)

		if err := p.storage.UpdateQueuedEmail(email); err != nil {
			return fmt.Errorf("Error updating queued email %s: %v", email.ID, err)
		}
		return nil
	}

	// Success - remove from queue
	if err := p.storage.RemoveFromQueue(email.ID); err != nil {
		return fmt.Errorf("Error removing email %s from queue: %v", email.ID, err)
	}
	log.Printf("Email %s delivered successfully to %s", email.ID, email.To)

	return nil
}

func (p *Processor) handlePermanentFailure(email *storage.QueuedEmail) {
	// Generate bounce message
	bounce := p.generateBounce(email)

	// Queue bounce to original sender
	if err := p.storage.QueueForRelay("", email.From, bounce); err != nil {
		log.Printf("Error queueing bounce for %s: %v", email.ID, err)
	}

	// Remove failed email from queue
	if err := p.storage.RemoveFromQueue(email.ID); err != nil {
		log.Printf("Error removing failed email %s: %v", email.ID, err)
	}
}

func (p *Processor) generateBounce(email *storage.QueuedEmail) []byte {
	bounce := "From: MAILER-DAEMON@" + email.From + "\r\n"
	bounce += "To: " + email.From + "\r\n"
	bounce += "Subject: Mail delivery failed: returning message to sender\r\n"
	bounce += "Content-Type: text/plain; charset=utf-8\r\n"
	bounce += "\r\n"
	bounce += "This message was created automatically by mail delivery software.\r\n\r\n"
	bounce += "A message that you sent could not be delivered to one or more of its\r\n"
	bounce += "recipients. This is a permanent error.\r\n\r\n"
	bounce += "Recipient: " + email.To + "\r\n"
	bounce += "Error: " + email.LastError + "\r\n"
	bounce += "\r\n"
	bounce += "--- Original message follows ---\r\n\r\n"
	bounce += string(email.Data)

	return []byte(bounce)
}
