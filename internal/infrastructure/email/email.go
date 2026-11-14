// Package email sends transactional email (verification, password reset). In development the
// log driver prints the message, including the link, instead of sending it.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Message is a plain-text email.
type Message struct {
	To, Subject, Body string
}

// Mailer delivers messages. Two implementations: SMTP (production) and Log (development/tests).
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// SMTP sends through an authenticated relay using implicit TLS or STARTTLS.
type SMTP struct {
	Host, Username, Password, From string
	Port                           int
	Secure                         bool        // implicit TLS, normally port 465
	tlsConfig                      *tls.Config // test-only trust roots; production uses system roots
}

func (s SMTP) connect(ctx context.Context) (*smtp.Client, error) {
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = conn.Close()
		}
	}()
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	if s.tlsConfig != nil {
		tlsConfig = s.tlsConfig.Clone()
	}
	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = s.Host
	}
	if tlsConfig.MinVersion == 0 {
		tlsConfig.MinVersion = tls.VersionTLS12
	}
	if s.Secure {
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return nil, err
	}
	if !s.Secure {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			_ = client.Close()
			return nil, errors.New("SMTP server does not offer STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	failed = false
	return client, nil
}

func (s SMTP) Send(ctx context.Context, m Message) error {
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("invalid mail sender: %w", err)
	}
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("invalid mail recipient: %w", err)
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("invalid mail subject")
	}
	var body bytes.Buffer
	encoded := quotedprintable.NewWriter(&body)
	if _, err := encoded.Write([]byte(m.Body)); err != nil {
		return err
	}
	if err := encoded.Close(); err != nil {
		return err
	}
	message := strings.Join([]string{
		"From: " + from.String(), "To: " + to.String(), "Subject: " + m.Subject,
		"Date: " + time.Now().Format(time.RFC1123Z), "Auto-Submitted: auto-generated",
		"MIME-Version: 1.0", "Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable", "", body.String(),
	}, "\r\n")
	client, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(message)); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	// DATA completion confirms relay acceptance. A failed QUIT must not cause a retry
	// that could send the same message twice.
	_ = client.Quit()
	return nil
}

// Log prints emails instead of sending them, and keeps the last ones for tests.
type Log struct {
	Logger *slog.Logger
	mu     sync.Mutex
	sent   []Message
}

func (l *Log) Send(_ context.Context, m Message) error {
	l.mu.Lock()
	l.sent = append(l.sent, m)
	l.mu.Unlock()
	if l.Logger != nil {
		l.Logger.Info("email (log driver)", "to", m.To, "subject", m.Subject, "body", m.Body)
	}
	return nil
}

// Sent returns a copy of every message (tests).
func (l *Log) Sent() []Message {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Message(nil), l.sent...)
}

// Templates ------------------------------------------------------------------

func Verification(appURL, to, name, token string) Message {
	link := fmt.Sprintf("%s/verify-email?token=%s", appURL, token)
	return Message{To: to, Subject: "Confirm your Finfe email", Body: fmt.Sprintf(
		"Hi%s,\n\nConfirm your email to finish setting up Finfe:\n\n%s\n\nThe link expires in 24 hours. If you didn't create an account, ignore this email.\n\nFinfe", greet(name), link)}
}

func PasswordReset(appURL, to, name, token string) Message {
	link := fmt.Sprintf("%s/reset?token=%s", appURL, token)
	return Message{To: to, Subject: "Reset your Finfe password", Body: fmt.Sprintf(
		"Hi%s,\n\nSomeone asked to reset your Finfe password. If it was you, choose a new one here:\n\n%s\n\nThe link expires in 1 hour and works once. If it wasn't you, ignore this email: your password stays the same.\n\nFinfe", greet(name), link)}
}

func greet(name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return " " + name
	}
	return ""
}
