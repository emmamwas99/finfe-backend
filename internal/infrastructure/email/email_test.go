package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSMTPImplicitTLSDeliversVerification(t *testing.T) {
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := certServer.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	certServer.Close()

	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		_, _ = fmt.Fprint(conn, "220 localhost ESMTP\r\n")
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO "):
				_, _ = fmt.Fprint(conn, "250-localhost\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(line, "AUTH PLAIN "):
				payload, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(line, "AUTH PLAIN ")))
				if string(payload) != "\x00mailer@example.com\x00test-secret" {
					_, _ = fmt.Fprint(conn, "535 bad credentials\r\n")
					return
				}
				_, _ = fmt.Fprint(conn, "235 authenticated\r\n")
			case strings.HasPrefix(line, "MAIL FROM:") || strings.HasPrefix(line, "RCPT TO:"):
				_, _ = fmt.Fprint(conn, "250 ok\r\n")
			case line == "DATA\r\n":
				_, _ = fmt.Fprint(conn, "354 send message\r\n")
				var message strings.Builder
				for {
					part, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if part == ".\r\n" {
						break
					}
					message.WriteString(part)
				}
				received <- message.String()
				_, _ = fmt.Fprint(conn, "250 queued\r\n")
			case line == "QUIT\r\n":
				_, _ = fmt.Fprint(conn, "221 bye\r\n")
				return
			default:
				_, _ = fmt.Fprint(conn, "500 unexpected command\r\n")
				return
			}
		}
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	mailer := SMTP{Host: host, Port: port, Secure: true, Username: "mailer@example.com", Password: "test-secret", From: "Finfe <mailer@example.com>", tlsConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mailer.Send(ctx, Verification("http://localhost:3000", "person@example.com", "Tess", "test-token")); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-received:
		parsed, err := mail.ReadMessage(strings.NewReader(message))
		if err != nil {
			t.Fatal(err)
		}
		from, err := mail.ParseAddress(parsed.Header.Get("From"))
		if err != nil {
			t.Fatal(err)
		}
		to, err := mail.ParseAddress(parsed.Header.Get("To"))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
		if err != nil {
			t.Fatal(err)
		}
		if from.Name != "Finfe" || from.Address != "mailer@example.com" {
			t.Errorf("sender = %s", from.String())
		}
		if to.Address != "person@example.com" {
			t.Errorf("recipient = %s", to.String())
		}
		if parsed.Header.Get("Subject") != "Confirm your Finfe email" {
			t.Error("wrong subject")
		}
		if !strings.Contains(string(body), "/verify-email?token=test-token") {
			t.Error("missing verification link")
		}
	case <-ctx.Done():
		t.Fatal("SMTP server did not receive a message")
	}
}

// Run explicitly with FINFE_LIVE_MAIL_TO and SMTP_* variables to verify the configured relay.
func TestSMTPLiveDelivery(t *testing.T) {
	to := os.Getenv("FINFE_LIVE_MAIL_TO")
	if to == "" {
		t.Skip("set FINFE_LIVE_MAIL_TO to send one live test email")
	}
	port, err := strconv.Atoi(os.Getenv("SMTP_PORT"))
	if err != nil {
		t.Fatal("SMTP_PORT must be an integer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mailer := SMTP{
		Host: os.Getenv("SMTP_HOST"), Port: port, Secure: os.Getenv("SMTP_SECURE") == "true",
		Username: os.Getenv("SMTP_USERNAME"), Password: os.Getenv("SMTP_PASSWORD"), From: os.Getenv("MAIL_FROM"),
	}
	if err := mailer.Send(ctx, Message{To: to, Subject: "Finfe email delivery test", Body: "This is the single test email requested for Finfe. No action is needed.\n\nFinfe"}); err != nil {
		t.Fatal(err)
	}
}
