// Package email delivers plain-text notification messages over SMTP.
package email

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"net"
	"net/mail"
	"net/netip"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/notify/httpx"
)

const defaultTimeout = 10 * time.Second

// Config defines one authenticated TLS SMTP destination.
type Config struct {
	Host         string
	Port         int
	TLSMode      core.NotificationTLSMode
	AuthMode     core.NotificationAuthMode
	Username     string
	Password     string
	From         string
	FromName     string
	Recipients   []string
	AllowPrivate bool
	Timeout      time.Duration
	TLSConfig    *tls.Config
	DialContext  func(context.Context, string, string) (net.Conn, error)
	Resolver     httpx.Resolver
	Dialer       httpx.Dialer
}

// Client delivers plain-text SMTP messages.
type Client struct{ config Config }

var _ core.NotificationChannel = (*Client)(nil)

// New validates and constructs an SMTP client.
func New(config Config) (*Client, error) {
	settings := core.NotificationSettings{
		SMTPPort: config.Port, TLSMode: config.TLSMode, AuthMode: config.AuthMode,
		Username: config.Username, FromAddress: config.From, FromName: config.FromName,
		Recipients: append([]string(nil), config.Recipients...),
	}
	if config.Password == "" || core.ValidateNotificationSettings(core.NotificationKindEmail, config.Host, settings) != nil {
		return nil, core.ErrInvalidArgument
	}
	if literal, ok := smtpHostLiteral(config.Host); ok && !httpx.AddressAllowed(literal, config.AllowPrivate) {
		return nil, core.ErrInvalidArgument
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	config.Recipients = append([]string(nil), config.Recipients...)
	return &Client{config: config}, nil
}

func smtpHostLiteral(host string) (netip.Addr, bool) {
	literal := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	address, err := netip.ParseAddr(literal)
	return address, err == nil
}

// Kind reports the adapter kind.
func (c *Client) Kind() core.NotificationKind { return core.NotificationKindEmail }

// Probe connects, authenticates, and sends a test message.
func (c *Client) Probe(ctx context.Context) error {
	deliveryID, err := core.NewID()
	if err != nil {
		return smtpFailure("message", false, err)
	}
	return c.Send(ctx, core.RenderedNotification{
		Subject: "Bloom test notification", PlainBody: "Bloom test notification",
		Payload: core.NotificationPayload{DeliveryID: deliveryID},
	})
}

// Send delivers one plain-text message.
func (c *Client) Send(ctx context.Context, message core.RenderedNotification) error {
	if !core.ValidID(message.Payload.DeliveryID) {
		return smtpFailure("message", false, core.ErrInvalidArgument)
	}
	connection, err := c.connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	client, err := c.smtpClient(connection)
	if err != nil {
		return smtpFailure("connect", true, err)
	}
	defer func() { _ = client.Close() }()
	if tlsErr := c.startTLS(client); tlsErr != nil {
		return tlsErr
	}
	if authErr := client.Auth(c.auth()); authErr != nil {
		return smtpFailure("authenticate", false, authErr)
	}
	if senderErr := client.Mail(c.config.From); senderErr != nil {
		return smtpFailure("sender", false, senderErr)
	}
	for _, recipient := range uniqueRecipients(c.config.Recipients) {
		if recipientErr := client.Rcpt(recipient); recipientErr != nil {
			return smtpFailure("recipient", false, recipientErr)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return smtpFailure("data", true, err)
	}
	if _, err := writer.Write(c.message(message)); err != nil {
		_ = writer.Close()
		return smtpFailure("write", true, err)
	}
	if err := writer.Close(); err != nil {
		return smtpFailure("send", true, err)
	}
	if err := client.Quit(); err != nil {
		return nil
	}
	return nil
}

func (c *Client) connect(ctx context.Context) (net.Conn, error) {
	address := net.JoinHostPort(c.config.Host, strconv.Itoa(c.config.Port))
	dial := c.config.DialContext
	if dial == nil {
		dial = httpx.SafeDialContext(c.config.Resolver, c.config.Dialer, c.config.AllowPrivate)
	}
	connection, err := dial(ctx, "tcp", address)
	if err != nil {
		return nil, smtpFailure("connect", true, err)
	}
	if c.config.TLSMode == core.NotificationTLSImplicit {
		connection, err = c.secureConnection(ctx, connection)
		if err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(c.config.Timeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := connection.SetDeadline(deadline); err != nil {
		_ = connection.Close()
		return nil, smtpFailure("deadline", true, err)
	}
	return connection, nil
}

func (c *Client) secureConnection(ctx context.Context, connection net.Conn) (net.Conn, error) {
	secure := tls.Client(connection, c.tlsConfig())
	if err := secure.HandshakeContext(ctx); err != nil {
		_ = connection.Close()
		return nil, smtpFailure("tls", true, err)
	}
	return secure, nil
}

func (c *Client) smtpClient(connection net.Conn) (*smtp.Client, error) {
	return smtp.NewClient(connection, c.config.Host)
}

func (c *Client) startTLS(client *smtp.Client) error {
	if c.config.TLSMode != core.NotificationTLSStartTLS {
		return nil
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return smtpFailure("starttls", false, errors.New("server does not advertise STARTTLS"))
	}
	if err := client.StartTLS(c.tlsConfig()); err != nil {
		return smtpFailure("starttls", true, err)
	}
	return nil
}

func (c *Client) tlsConfig() *tls.Config {
	if c.config.TLSConfig != nil {
		result := c.config.TLSConfig.Clone()
		result.ServerName = c.config.Host
		result.MinVersion = max(result.MinVersion, tls.VersionTLS12)
		return result
	}
	return &tls.Config{ServerName: c.config.Host, MinVersion: tls.VersionTLS12}
}

func (c *Client) auth() smtp.Auth {
	if c.config.AuthMode == core.NotificationAuthLogin {
		return loginAuth{username: c.config.Username, password: c.config.Password}
	}
	return smtp.PlainAuth("", c.config.Username, c.config.Password, c.config.Host)
}

func (c *Client) message(message core.RenderedNotification) []byte {
	from := (&mail.Address{Name: c.config.FromName, Address: c.config.From}).String()
	subject := strings.NewReplacer("\r", " ", "\n", " ").Replace(message.Subject)
	headers := []string{
		"From: " + from, "To: " + strings.Join(c.config.Recipients, ", "),
		"Message-ID: <" + message.Payload.DeliveryID + "@bloom>",
		"Subject: " + subject, "MIME-Version: 1.0", "Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit", "", strings.ReplaceAll(message.PlainBody, "\n", "\r\n"),
	}
	return []byte(strings.Join(headers, "\r\n"))
}

func smtpFailure(operation string, retryable bool, err error) error {
	if response, ok := errors.AsType[*textproto.Error](err); ok {
		retryable = response.Code >= 400 && response.Code < 500
	}
	kind := core.NotificationUnavailable
	if !retryable {
		kind = core.NotificationRejected
	}
	return &core.NotificationError{Kind: kind, Operation: operation, Retryable: retryable, Err: err}
}

func uniqueRecipients(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

type loginAuth struct{ username, password string }

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("LOGIN authentication requires TLS")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(string(challenge))
	if err != nil {
		decoded = challenge
	}
	prompt := strings.ToLower(string(decoded))
	if strings.Contains(prompt, "user") {
		return []byte(a.username), nil
	}
	if strings.Contains(prompt, "pass") {
		return []byte(a.password), nil
	}
	return nil, errors.New("unexpected LOGIN challenge")
}
