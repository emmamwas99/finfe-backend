// Package email sends transactional email (verification, password reset). In development the
// log driver prints the message, including the link, instead of sending it.
package email

import (
	"context"
	"fmt"
	"log/slog"
	"net/smtp"
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

// SMTP sends through an SMTP relay with STARTTLS (e.g. Resend, Brevo, Postmark, SES).
type SMTP struct {
	Host, Username, Password, From string
	Port                           int
}

func (s SMTP) Send(_ context.Context, m Message) error {
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}
	msg := strings.Join([]string{
		"From: " + s.From, "To: " + m.To, "Subject: " + m.Subject,
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0", "Content-Type: text/plain; charset=UTF-8", "", m.Body,
	}, "\r\n")
	return smtp.SendMail(addr, auth, extractAddress(s.From), []string{m.To}, []byte(msg))
}

func extractAddress(from string) string {
	if i := strings.Index(from, "<"); i >= 0 {
		return strings.Trim(from[i:], "<>")
	}
	return from
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
