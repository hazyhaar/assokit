// CLAUDE:SUMMARY Backend SMTPS (port 465 implicit TLS) — utilisé pour OVH Email Pro.
package mailer

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/smtp"
	"strconv"
	"time"
)

func base64StdEncoding(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// sendSMTP envoie msg via SMTPS (TLS implicit, port 465 par défaut).
// Met à jour email_outbox status='sent' en cas de succès, applique backoff sinon.
func (m *Mailer) sendSMTP(ctx context.Context, msg OutboxMsg) error {
	port := m.SMTPPort
	if port == 0 {
		port = 465
	}
	addr := m.SMTPHost + ":" + strconv.Itoa(port)

	tlsCfg := &tls.Config{ServerName: m.SMTPHost, MinVersion: tls.VersionTLS12}
	dialer := &tls.Dialer{Config: tlsCfg, NetDialer: nil}

	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		m.applyBackoff(ctx, msg, "smtp dial: "+err.Error())
		return fmt.Errorf("sendSMTP dial: %w", err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, m.SMTPHost)
	if err != nil {
		m.applyBackoff(ctx, msg, "smtp client: "+err.Error())
		return fmt.Errorf("sendSMTP client: %w", err)
	}
	defer c.Close()

	if err := c.Hello("localhost"); err != nil {
		m.applyBackoff(ctx, msg, "smtp hello: "+err.Error())
		return fmt.Errorf("sendSMTP hello: %w", err)
	}

	auth := smtp.PlainAuth("", m.SMTPUser, m.SMTPPass, m.SMTPHost)
	if err := c.Auth(auth); err != nil {
		// Auth fail = config erronée, pas backoff (sinon retry indéfini sur même erreur).
		// On laisse pending et on log.
		m.log().Error("sendSMTP auth failed (config invalide)", "id", msg.ID, "err", err)
		return fmt.Errorf("sendSMTP auth: %w", err)
	}

	if err := c.Mail(m.From); err != nil {
		m.applyBackoff(ctx, msg, "smtp mail-from: "+err.Error())
		return fmt.Errorf("sendSMTP MAIL FROM: %w", err)
	}
	if err := c.Rcpt(msg.ToAddr); err != nil {
		// Rcpt invalide = adresse destinataire invalide, pas backoff.
		m.log().Error("sendSMTP rcpt rejected (adresse invalide ?)", "id", msg.ID, "to", msg.ToAddr, "err", err)
		return fmt.Errorf("sendSMTP RCPT TO: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		m.applyBackoff(ctx, msg, "smtp data: "+err.Error())
		return fmt.Errorf("sendSMTP DATA: %w", err)
	}
	rfc := buildRFC822(m.From, msg)
	if _, err := w.Write([]byte(rfc)); err != nil {
		w.Close() //nolint:errcheck
		m.applyBackoff(ctx, msg, "smtp write: "+err.Error())
		return fmt.Errorf("sendSMTP write: %w", err)
	}
	if err := w.Close(); err != nil {
		m.applyBackoff(ctx, msg, "smtp close: "+err.Error())
		return fmt.Errorf("sendSMTP close: %w", err)
	}
	if err := c.Quit(); err != nil {
		// QUIT fail post-data = email parti quand-même, on log mais on marque sent.
		m.log().Warn("sendSMTP QUIT err (email envoyé quand-même)", "id", msg.ID, "err", err)
	}

	_, dbErr := m.DB.ExecContext(ctx,
		`UPDATE email_outbox SET status='sent', sent_at=CURRENT_TIMESTAMP WHERE id=?`,
		msg.ID,
	)
	return dbErr
}

// buildRFC822 construit un message RFC822 multipart/alternative (text + html).
func buildRFC822(from string, msg OutboxMsg) string {
	boundary := "boundary-" + msg.ID
	var b []byte
	b = append(b, ("From: "+from+"\r\n")...)
	b = append(b, ("To: "+msg.ToAddr+"\r\n")...)
	b = append(b, ("Subject: "+encodeHeader(msg.Subject)+"\r\n")...)
	b = append(b, "MIME-Version: 1.0\r\n"...)
	b = append(b, ("Content-Type: multipart/alternative; boundary=\""+boundary+"\"\r\n\r\n")...)
	if msg.BodyText != "" {
		b = append(b, ("--"+boundary+"\r\n")...)
		b = append(b, "Content-Type: text/plain; charset=UTF-8\r\n\r\n"...)
		b = append(b, msg.BodyText...)
		b = append(b, "\r\n"...)
	}
	if msg.BodyHTML != "" {
		b = append(b, ("--"+boundary+"\r\n")...)
		b = append(b, "Content-Type: text/html; charset=UTF-8\r\n\r\n"...)
		b = append(b, msg.BodyHTML...)
		b = append(b, "\r\n"...)
	}
	b = append(b, ("--"+boundary+"--\r\n")...)
	return string(b)
}

// encodeHeader RFC2047 (UTF-8 base64) si non-ASCII détecté, sinon brut.
func encodeHeader(s string) string {
	for _, r := range s {
		if r > 127 {
			return mimeWord(s)
		}
	}
	return s
}

func mimeWord(s string) string {
	const start = "=?utf-8?b?"
	const end = "?="
	enc := base64StdEncoding(s)
	return start + enc + end
}
