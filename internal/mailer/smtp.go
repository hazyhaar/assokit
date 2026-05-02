// CLAUDE:SUMMARY Backend SMTPS (port 465 implicit TLS) — utilisé pour OVH Email Pro.
package mailer

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

func base64StdEncoding(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// sendSMTP envoie msg via SMTP. Modes selon SMTPPort :
//   - 465 (default) : TLS implicit (SMTPS) — bloqué par OVH outbound sur Kimsufi
//   - 587           : plain TCP + STARTTLS (submission) — ouvert OVH outbound
//   - 25            : plain TCP + STARTTLS si supporté (SMTP standard)
//
// Met à jour email_outbox status='sent' en cas de succès, applique backoff sinon.
func (m *Mailer) sendSMTP(ctx context.Context, msg OutboxMsg) error {
	port := m.SMTPPort
	if port == 0 {
		port = 465
	}
	addr := m.SMTPHost + ":" + strconv.Itoa(port)
	tlsCfg := &tls.Config{ServerName: m.SMTPHost, MinVersion: tls.VersionTLS12}

	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var conn net.Conn
	var err error
	if port == 465 {
		// TLS implicit dès le début.
		dialer := &tls.Dialer{Config: tlsCfg, NetDialer: nil}
		conn, err = dialer.DialContext(dialCtx, "tcp", addr)
	} else {
		// Plain TCP — STARTTLS appliqué juste après HELO.
		var d net.Dialer
		conn, err = d.DialContext(dialCtx, "tcp", addr)
	}
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

	// STARTTLS pour ports submission (587) ou SMTP (25).
	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				m.applyBackoff(ctx, msg, "smtp starttls: "+err.Error())
				return fmt.Errorf("sendSMTP STARTTLS: %w", err)
			}
		}
	}

	// OVH SMTPS exige AUTH LOGIN (refuse AUTH PLAIN avec 504 5.7.4).
	// On essaye LOGIN d'abord ; si pas dispo, fallback sur PLAIN.
	var auth smtp.Auth
	if ok, mechs := c.Extension("AUTH"); ok && containsAuth(mechs, "LOGIN") {
		auth = &loginAuth{username: m.SMTPUser, password: m.SMTPPass}
	} else {
		auth = smtp.PlainAuth("", m.SMTPUser, m.SMTPPass, m.SMTPHost)
	}
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
		// QUIT post-data : si erreur 5xx, le serveur a rejeté le message après DATA
		// (cas OVH 550 sender mismatch parfois renvoyé en QUIT). NE PAS marquer sent.
		errStr := err.Error()
		if strings.Contains(errStr, "550") || strings.Contains(errStr, "551") ||
			strings.Contains(errStr, "552") || strings.Contains(errStr, "553") ||
			strings.Contains(errStr, "554") {
			m.applyBackoff(ctx, msg, "smtp quit rejected: "+errStr)
			return fmt.Errorf("sendSMTP QUIT rejected: %w", err)
		}
		m.log().Warn("sendSMTP QUIT err non-5xx (email présumé envoyé)", "id", msg.ID, "err", err)
	}

	// Clear last_error résiduel d'attempts précédents : status=sent doit refléter
	// un état propre (rule MEGA-AUDIT-HONESTY : pas de divergence status/last_error).
	_, dbErr := m.DB.ExecContext(ctx,
		`UPDATE email_outbox SET status='sent', sent_at=CURRENT_TIMESTAMP, last_error='' WHERE id=?`,
		msg.ID,
	)
	return dbErr
}

// buildRFC822 construit un message RFC822 multipart/alternative (text + html).
func buildRFC822(from string, msg OutboxMsg) string {
	boundary := "boundary-" + msg.ID
	var b []byte
	b = append(b, ("From: " + from + "\r\n")...)
	b = append(b, ("To: " + msg.ToAddr + "\r\n")...)
	b = append(b, ("Subject: " + encodeHeader(msg.Subject) + "\r\n")...)
	b = append(b, "MIME-Version: 1.0\r\n"...)
	b = append(b, ("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")...)
	if msg.BodyText != "" {
		b = append(b, ("--" + boundary + "\r\n")...)
		b = append(b, "Content-Type: text/plain; charset=UTF-8\r\n\r\n"...)
		b = append(b, msg.BodyText...)
		b = append(b, "\r\n"...)
	}
	if msg.BodyHTML != "" {
		b = append(b, ("--" + boundary + "\r\n")...)
		b = append(b, "Content-Type: text/html; charset=UTF-8\r\n\r\n"...)
		b = append(b, msg.BodyHTML...)
		b = append(b, "\r\n"...)
	}
	b = append(b, ("--" + boundary + "--\r\n")...)
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

// loginAuth implémente smtp.Auth pour AUTH LOGIN (compatible OVH).
type loginAuth struct {
	username, password string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := string(fromServer)
	switch prompt {
	case "Username:", "username:", "User Name\x00":
		return []byte(a.username), nil
	case "Password:", "password:", "Password\x00":
		return []byte(a.password), nil
	}
	// Fallback : 1er challenge → username, 2e → password.
	if a.username != "" {
		u := a.username
		a.username = ""
		return []byte(u), nil
	}
	return []byte(a.password), nil
}

func containsAuth(mechs, want string) bool {
	for _, m := range strings.Fields(mechs) {
		if strings.EqualFold(m, want) {
			return true
		}
	}
	return false
}
