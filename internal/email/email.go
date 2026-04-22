package email

import (
	"fmt"
	"net/smtp"
)

// Sender delivers a single email message. Implementations include smtpSender
// and test doubles (see testutil/email.go).
type Sender interface {
	Send(to, subject, body string) error
}

// smtpSender delivers via stdlib net/smtp using PLAIN auth.
type smtpSender struct {
	host string // host is the SMTP server hostname
	port string // port is the SMTP server port
	user string // user is the PLAIN-auth username (may be empty)
	pass string // pass is the PLAIN-auth password (may be empty)
	from string // from is the From: header value
}

// NewSMTPSender constructs an SMTP-backed Sender.
func NewSMTPSender(host, port, user, pass, from string) Sender {
	return &smtpSender{host: host, port: port, user: user, pass: pass, from: from}
}

// Send delivers a plain-text message to a single recipient.
func (s *smtpSender) Send(to, subject, body string) error {
	addr := s.host + ":" + s.port
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s", s.from, to, subject, body))
	var auth smtp.Auth
	if s.user != "" {
		auth = smtp.PlainAuth("", s.user, s.pass, s.host)
	}
	if err := smtp.SendMail(addr, auth, s.from, []string{to}, msg); err != nil {
		return fmt.Errorf("smtp send:\n%w", err)
	}
	return nil
}

// Message captures a single sent email (used by Recorder).
type Message struct {
	To      string // To is the recipient address
	Subject string // Subject is the message subject line
	Body    string // Body is the plain-text body
}

// Recorder is an in-memory Sender used in tests and in the ENV=test config.
type Recorder struct {
	sent []Message // sent is the ordered list of messages captured
}

// NewRecorder constructs an empty Recorder.
func NewRecorder() *Recorder { return &Recorder{} }

// Send captures the message instead of delivering it.
func (r *Recorder) Send(to, subject, body string) error {
	r.sent = append(r.sent, Message{To: to, Subject: subject, Body: body})
	return nil
}

// Sent returns a copy of the captured messages in order.
func (r *Recorder) Sent() []Message {
	out := make([]Message, len(r.sent))
	copy(out, r.sent)
	return out
}
