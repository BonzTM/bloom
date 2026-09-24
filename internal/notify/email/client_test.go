package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"net/netip"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type smtpFixture struct {
	listener net.Listener
	tls      *tls.Config
	address  string
	done     chan error
	messages chan string
	rcpts    chan []string
}

func newSMTPFixture(t *testing.T, implicit bool) *smtpFixture {
	t.Helper()
	return newSMTPFixtureMode(t, implicit, false)
}

func newSMTPFixtureMode(t *testing.T, implicit, closeAfterData bool) *smtpFixture {
	t.Helper()
	certificateServer := httptest.NewTLSServer(nil)
	certificate := certificateServer.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(certificateServer.Certificate())
	certificateServer.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverTLS := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	if implicit {
		listener = tls.NewListener(listener, serverTLS)
	}
	fixture := &smtpFixture{
		listener: listener, tls: serverTLS, address: listener.Addr().String(), done: make(chan error, 1),
		messages: make(chan string, 1), rcpts: make(chan []string, 1),
	}
	fixture.tls.RootCAs = pool
	go fixture.serve(!implicit, closeAfterData)
	t.Cleanup(func() { _ = listener.Close() })
	return fixture
}

func (s *smtpFixture) serve(startTLS, closeAfterData bool) {
	connection, err := s.listener.Accept()
	if err != nil {
		s.done <- err
		return
	}
	defer func() { _ = connection.Close() }()
	s.done <- serveSMTPSession(connection, s.tls, startTLS, closeAfterData, s.messages, s.rcpts)
}

func serveSMTPSession(
	connection net.Conn, tlsConfig *tls.Config, startTLS, closeAfterData bool,
	messages chan<- string, recipients chan<- []string,
) error {
	session := &smtpTestSession{
		connection: connection, reader: bufio.NewReader(connection), writer: bufio.NewWriter(connection),
		tls: tlsConfig, startTLS: startTLS, closeAfterData: closeAfterData,
		messages: messages, recipients: recipients, accepted: make([]string, 0, core.MaxNotificationRecipients),
	}
	if err := smtpReply(session.writer, "220 example.com ESMTP ready"); err != nil {
		return err
	}
	for range 32 {
		line, err := session.reader.ReadString('\n')
		if err != nil {
			return err
		}
		done, err := session.handle(strings.TrimSpace(line))
		if err != nil || done {
			return err
		}
	}
	return errors.New("SMTP command limit exceeded")
}

type smtpTestSession struct {
	connection     net.Conn
	reader         *bufio.Reader
	writer         *bufio.Writer
	tls            *tls.Config
	startTLS       bool
	closeAfterData bool
	messages       chan<- string
	recipients     chan<- []string
	accepted       []string
}

func (s *smtpTestSession) handle(command string) (bool, error) {
	switch {
	case strings.HasPrefix(command, "EHLO"):
		capabilities := "250-example.com\r\n250-AUTH PLAIN LOGIN\r\n250 OK"
		if s.startTLS {
			capabilities = "250-example.com\r\n250-STARTTLS\r\n250-AUTH PLAIN LOGIN\r\n250 OK"
		}
		return false, smtpReply(s.writer, capabilities)
	case command == "STARTTLS" && s.startTLS:
		return false, s.upgradeTLS()
	case strings.HasPrefix(command, "AUTH PLAIN"):
		return false, smtpReply(s.writer, "235 authenticated")
	case command == "AUTH LOGIN":
		return false, smtpLogin(s.reader, s.writer)
	case strings.HasPrefix(command, "MAIL FROM:"):
		return false, smtpReply(s.writer, "250 accepted")
	case strings.HasPrefix(command, "RCPT TO:"):
		s.accepted = append(s.accepted, command)
		return false, smtpReply(s.writer, "250 accepted")
	case command == "DATA":
		message, err := smtpData(s.reader, s.writer)
		if err == nil {
			s.messages <- message
			s.recipients <- append([]string(nil), s.accepted...)
		}
		return s.closeAfterData, err
	case command == "QUIT":
		return true, smtpReply(s.writer, "221 goodbye")
	default:
		return false, fmt.Errorf("unexpected SMTP command %q", command)
	}
}

func (s *smtpTestSession) upgradeTLS() error {
	if err := smtpReply(s.writer, "220 Begin TLS"); err != nil {
		return err
	}
	secure := tls.Server(s.connection, s.tls)
	if err := secure.Handshake(); err != nil {
		return err
	}
	s.connection, s.reader, s.writer = secure, bufio.NewReader(secure), bufio.NewWriter(secure)
	s.startTLS = false
	return nil
}

func TestSendSucceedsWhenServerClosesAfterAcceptingData(t *testing.T) {
	t.Parallel()
	fixture := newSMTPFixtureMode(t, false, true)
	client := fixtureClient(t, fixture, core.NotificationTLSStartTLS, core.NotificationAuthPlain)
	if err := client.Send(t.Context(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if message := <-fixture.messages; !strings.Contains(message, "Message-ID: <00000000-0000-4000-8000-000000000099@bloom>\r\n") {
		t.Fatalf("message lacks delivery Message-ID: %q", message)
	}
	if err := <-fixture.done; err != nil {
		t.Fatalf("SMTP server: %v", err)
	}
}

func fixtureClient(
	t *testing.T, fixture *smtpFixture, tlsMode core.NotificationTLSMode, authMode core.NotificationAuthMode,
) *Client {
	t.Helper()
	_, portText, err := net.SplitHostPort(fixture.address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{
		Host: "example.com", Port: port, TLSMode: tlsMode, AuthMode: authMode,
		Username: "user", Password: "password", From: "from@example.com", FromName: "Bloom",
		Recipients: []string{"to@example.com"}, Timeout: time.Second, TLSConfig: fixture.tls,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, fixture.address)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func smtpLogin(reader *bufio.Reader, writer *bufio.Writer) error {
	for _, prompt := range []string{"Username:", "Password:"} {
		challenge := base64.StdEncoding.EncodeToString([]byte(prompt))
		if err := smtpReply(writer, "334 "+challenge); err != nil {
			return err
		}
		if _, err := reader.ReadString('\n'); err != nil {
			return err
		}
	}
	return smtpReply(writer, "235 authenticated")
}

func smtpData(reader *bufio.Reader, writer *bufio.Writer) (string, error) {
	if err := smtpReply(writer, "354 end with dot"); err != nil {
		return "", err
	}
	var message strings.Builder
	for range 128 {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		if line == ".\r\n" {
			return message.String(), smtpReply(writer, "250 queued")
		}
		message.WriteString(line)
	}
	return "", errors.New("SMTP message line limit exceeded")
}

func smtpReply(writer *bufio.Writer, value string) error {
	if _, err := writer.WriteString(value + "\r\n"); err != nil {
		return err
	}
	return writer.Flush()
}

func TestSendSupportsTLSAndAuthenticationModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		tlsMode  core.NotificationTLSMode
		authMode core.NotificationAuthMode
		implicit bool
	}{
		{name: "STARTTLS PLAIN", tlsMode: core.NotificationTLSStartTLS, authMode: core.NotificationAuthPlain},
		{name: "implicit TLS LOGIN", tlsMode: core.NotificationTLSImplicit, authMode: core.NotificationAuthLogin, implicit: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			fixture := newSMTPFixture(t, testCase.implicit)
			_, portText, err := net.SplitHostPort(fixture.address)
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				t.Fatal(err)
			}
			client, err := New(Config{
				Host: "example.com", Port: port, TLSMode: testCase.tlsMode, AuthMode: testCase.authMode,
				Username: "user", Password: "password", From: "from@example.com", FromName: "Bloom",
				Recipients: []string{"to@example.com"}, Timeout: time.Second, TLSConfig: fixture.tls,
				DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, fixture.address)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Send(t.Context(), testMessage()); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if err := <-fixture.done; err != nil {
				t.Fatalf("SMTP server: %v", err)
			}
		})
	}
}

func testMessage() core.RenderedNotification {
	return core.RenderedNotification{
		Subject: "Subject", PlainBody: "Body",
		Payload: core.NotificationPayload{DeliveryID: "00000000-0000-4000-8000-000000000099"},
	}
}

func TestNewRejectsMoreThanThirtyTwoRecipients(t *testing.T) {
	t.Parallel()
	recipients := make([]string, core.MaxNotificationRecipients+1)
	for index := range recipients {
		recipients[index] = fmt.Sprintf("user%d@example.com", index)
	}
	_, err := New(Config{
		Host: "example.com", Port: 465, TLSMode: core.NotificationTLSImplicit,
		AuthMode: core.NotificationAuthPlain, Username: "user", Password: "password",
		From: "from@example.com", Recipients: recipients,
	})
	if err == nil {
		t.Fatal("New accepted too many recipients")
	}
}

func TestSendDoesNotIssueDuplicateRecipientCommands(t *testing.T) {
	t.Parallel()
	fixture := newSMTPFixture(t, false)
	client := fixtureClient(t, fixture, core.NotificationTLSStartTLS, core.NotificationAuthPlain)
	client.config.Recipients = []string{"Alice@example.com", "alice@EXAMPLE.COM"}
	if err := client.Send(t.Context(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if recipients := <-fixture.rcpts; len(recipients) != 1 {
		t.Fatalf("RCPT commands = %v", recipients)
	}
	if err := <-fixture.done; err != nil {
		t.Fatalf("SMTP server: %v", err)
	}
}

func TestSMTPFailureClassifiesProviderReplies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code      int
		retryable bool
	}{
		{code: 421, retryable: true},
		{code: 450, retryable: true},
		{code: 500},
		{code: 550},
	}
	for _, testCase := range tests {
		err := smtpFailure("recipient", !testCase.retryable, &textproto.Error{Code: testCase.code, Msg: "reply"})
		var classified *core.NotificationError
		if !errors.As(err, &classified) || classified.Retryable != testCase.retryable {
			t.Errorf("SMTP %d error = %#v", testCase.code, err)
		}
	}
}

func TestConnectionAndHandshakeFailuresAreRetryable(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name string
		mode core.NotificationTLSMode
		dial func(context.Context, string, string) (net.Conn, error)
	}{
		{name: "connection", mode: core.NotificationTLSStartTLS, dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("connection failed")
		}},
		{name: "TLS handshake", mode: core.NotificationTLSImplicit, dial: closedPipe},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			client := failureClient(t, testCase.mode, testCase.dial)
			var classified *core.NotificationError
			if err := client.Send(t.Context(), testMessage()); !errors.As(err, &classified) || !classified.Retryable {
				t.Fatalf("Send error = %#v", err)
			}
		})
	}
}

func failureClient(
	t *testing.T, mode core.NotificationTLSMode, dial func(context.Context, string, string) (net.Conn, error),
) *Client {
	t.Helper()
	client, err := New(Config{
		Host: "example.com", Port: 465, TLSMode: mode, AuthMode: core.NotificationAuthPlain,
		Username: "user", Password: "password", From: "from@example.com",
		Recipients: []string{"to@example.com"}, DialContext: dial,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func closedPipe(context.Context, string, string) (net.Conn, error) {
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

type smtpResolver map[string][]netip.Addr

func (r smtpResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return r[host], nil
}

type pipeDialer struct {
	calls int
	peer  net.Conn
}

func (d *pipeDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls++
	client, server := net.Pipe()
	d.peer = server
	return client, nil
}

func TestSMTPDialPolicyNeverAllowsLoopback(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"127.0.0.1", "::1", "[::1]"} {
		if _, err := newPolicyClient(host, true, nil, nil); !errors.Is(err, core.ErrInvalidArgument) {
			t.Fatalf("literal host %q: error=%v", host, err)
		}
	}
	dialer := &pipeDialer{}
	client := policyClient(t, "local.test", true, smtpResolver{
		"local.test": {netip.MustParseAddr("127.0.0.1")},
	}, dialer)
	if connection, err := client.connect(t.Context()); err == nil || connection != nil || dialer.calls != 0 {
		t.Fatalf("resolved loopback: connection=%v error=%v calls=%d", connection, err, dialer.calls)
	}
}

func TestSMTPDialPolicyAllowsOnlyOptedInPrivateAddresses(t *testing.T) {
	t.Parallel()
	resolver := smtpResolver{"private.test": {netip.MustParseAddr("10.0.0.5")}}
	denied := &pipeDialer{}
	if connection, err := policyClient(t, "private.test", false, resolver, denied).connect(t.Context()); err == nil || connection != nil {
		t.Fatalf("private without opt-in: connection=%v error=%v", connection, err)
	}
	allowed := &pipeDialer{}
	connection, err := policyClient(t, "private.test", true, resolver, allowed).connect(t.Context())
	if err != nil || connection == nil || allowed.calls != 1 {
		t.Fatalf("private with opt-in: connection=%v error=%v calls=%d", connection, err, allowed.calls)
	}
	_ = connection.Close()
	_ = allowed.peer.Close()
}

func policyClient(t *testing.T, host string, allowPrivate bool, resolver smtpResolver, dialer *pipeDialer) *Client {
	t.Helper()
	client, err := newPolicyClient(host, allowPrivate, resolver, dialer)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func newPolicyClient(host string, allowPrivate bool, resolver smtpResolver, dialer *pipeDialer) (*Client, error) {
	return New(Config{
		Host: host, Port: 587, TLSMode: core.NotificationTLSStartTLS, AuthMode: core.NotificationAuthPlain,
		Username: "user", Password: "password", From: "from@example.com",
		Recipients: []string{"to@example.com"}, AllowPrivate: allowPrivate,
		Resolver: resolver, Dialer: dialer,
	})
}
