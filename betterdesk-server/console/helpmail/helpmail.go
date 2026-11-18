// Package helpmail sends a mail for each help request a device raises:
// to the operators on the access lists of the device's folder and groups,
// or, when there are none, to the alert address of the SMTP settings.
// Which of that happens is the commercialization email configuration
// (the settings row commercialization_email_config), which the billing
// page edits. A recipient gets one mail per request: the notification
// log (email_notification_log) remembers who was told.
//
// The service listens to the server's event bus in the same process, for
// the events of the type help_request (Run).
package helpmail

import (
	"context"
	"encoding/json"
	"regexp"
	"sync"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/mail"
	"github.com/unitronix/betterdesk-server/console/store"
)

// ConfigKey is the settings row of the configuration.
const ConfigKey = "commercialization_email_config"

// eventType is the type of the bus events, and the event type of the
// notification log.
const eventType = "help_request"

// Config is the commercialization email configuration, with the keys and
// key order of its JSON in the settings row and in the routes' answers.
type Config struct {
	HelpRequestsEnabled     bool `json:"help_requests_enabled"`
	NotifyAssignedOperators bool `json:"notify_assigned_operators"`
	FallbackAlertEmail      bool `json:"fallback_alert_email"`
	IncludeFolderInSubject  bool `json:"include_folder_in_subject"`
}

// DefaultConfig has everything on.
func DefaultConfig() Config {
	return Config{HelpRequestsEnabled: true, NotifyAssignedOperators: true, FallbackAlertEmail: true, IncludeFolderInSubject: true}
}

// ConfigFrom takes a configuration out of a JSON value: each switch is
// on unless the value has it as false. A value without members (a list,
// a text, a number) has everything on.
func ConfigFrom(v any) Config {
	on := func(key string) bool {
		o, ok := v.(*goapi.Object)
		if !ok || !o.Has(key) {
			return true
		}
		value, isBool := o.Get(key).(bool)
		return !isBool || value
	}
	return Config{
		HelpRequestsEnabled:     on("help_requests_enabled"),
		NotifyAssignedOperators: on("notify_assigned_operators"),
		FallbackAlertEmail:      on("fallback_alert_email"),
		IncludeFolderInSubject:  on("include_folder_in_subject"),
	}
}

// ParseConfig reads the stored text of the configuration: the defaults
// when there is none, when it is empty, when it is no JSON and when it is
// null.
func ParseConfig(raw string, ok bool) Config {
	if !ok || raw == "" {
		return DefaultConfig()
	}
	v, err := goapi.Parse([]byte(raw))
	if err != nil || v == nil {
		return DefaultConfig()
	}
	return ConfigFrom(v)
}

// LoadConfig reads the configuration from the settings table.
func LoadConfig(ctx context.Context, q store.Querier) (Config, error) {
	raw, ok, err := getSetting(ctx, q, ConfigKey)
	if err != nil {
		return Config{}, err
	}
	return ParseConfig(raw, ok), nil
}

// SaveConfig stores the configuration as its JSON.
func SaveConfig(ctx context.Context, q store.Querier, c Config) error {
	text, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return setSetting(ctx, q, ConfigKey, string(text))
}

// Mailer is what the service needs of the mail service (*mail.Service).
type Mailer interface {
	GetAlertEmail(ctx context.Context) (string, error)
	SendHelpRequestEmail(ctx context.Context, m mail.HelpRequestEmail) (bool, error)
	HasEmailNotificationSent(ctx context.Context, eventType, eventID, recipient string) (bool, error)
	LogEmailNotificationSent(ctx context.Context, eventType, eventID, recipient string) error
}

// Logger takes the lines the service logs. *kit.Logger is one.
type Logger interface {
	Info(args ...any)
	Warn(args ...any)
	Error(args ...any)
}

// Options are the settings of a Service.
type Options struct {
	// DB holds the configuration.
	DB *store.DB
	// Mail sends the mails and keeps the notification log.
	Mail Mailer
	// Events subscribes to the server's event bus (goapi.Client.Events).
	Events func(ctx context.Context, filter string) (<-chan *goapi.Object, error)
	// FolderName and OperatorEmails find the device's folder and the
	// addresses of the operators allowed on it. Nil means the functions
	// of package devicegroups.
	FolderName     func(ctx context.Context, deviceID string) (string, error)
	OperatorEmails func(ctx context.Context, deviceID string) ([]devicegroups.OperatorEmail, error)
	// Logger takes what the service logs, under "HelpRequestEmail".
	Logger Logger
}

// Service sends the help request mails.
type Service struct {
	opts Options
	// running are the events being handled, which Run waits for.
	running sync.WaitGroup
}

// New returns a Service. It starts nothing.
func New(opts Options) *Service {
	if opts.FolderName == nil {
		opts.FolderName = func(_ context.Context, id string) (string, error) {
			return devicegroups.ResolveFolderNameForDevice(id)
		}
	}
	if opts.OperatorEmails == nil {
		opts.OperatorEmails = func(_ context.Context, id string) ([]devicegroups.OperatorEmail, error) {
			return devicegroups.ResolveOperatorEmailsForDevice(id)
		}
	}
	return &Service{opts: opts}
}

// Run listens for help request events until ctx ends, and handles each
// one beside the others; a failure is logged. It returns when ctx has
// ended and the events it took are handled. Without an event bus it logs
// that and returns.
func (s *Service) Run(ctx context.Context) {
	frames, err := s.opts.Events(ctx, eventType)
	if err != nil {
		s.opts.Logger.Warn("Event bus not available, help request email disabled")
		return
	}
	s.opts.Logger.Info("Help request email service initialized")
	for frame := range frames {
		if kind, _ := frame.Get("type").(string); kind != eventType {
			continue
		}
		data := frame.Get("data")
		if !goapi.Truthy(data) {
			data = goapi.NewObject()
		}
		s.running.Add(1)
		go func() {
			defer s.running.Done()
			if err := s.HandleEvent(ctx, data); err != nil {
				s.opts.Logger.Error("Failed to process help request event: " + sanitizeLogMessage(jsbind.Message(err)))
			}
		}()
	}
	s.running.Wait()
}

// HandleEvent sends the mails for one help request event, whose data has
// the request's id (or request_id), device_id, hostname and message.
// An event without id or device does nothing, and so does one while
// help request mails are off.
func (s *Service) HandleEvent(ctx context.Context, data any) error {
	config, err := LoadConfig(ctx, s.opts.DB)
	if err != nil {
		return err
	}
	if !config.HelpRequestsEnabled {
		return nil
	}
	requestID := jsbind.Trim(firstText(data, "id", "request_id"))
	deviceID := jsbind.Trim(firstText(data, "device_id"))
	message := jsbind.Trim(firstText(data, "message"))
	hostname := jsbind.Trim(orText(firstText(data, "hostname"), orText(deviceID, "Unknown device")))
	if requestID == "" || deviceID == "" {
		return nil
	}

	folder, err := s.opts.FolderName(ctx, deviceID)
	if err != nil {
		return err
	}
	var recipients []string
	if config.NotifyAssignedOperators {
		operators, err := s.opts.OperatorEmails(ctx, deviceID)
		if err != nil {
			return err
		}
		for _, operator := range operators {
			recipients = append(recipients, operator.Email)
		}
	}
	if len(recipients) == 0 && config.FallbackAlertEmail {
		alert, err := s.opts.Mail.GetAlertEmail(ctx)
		if err != nil {
			return err
		}
		if alert != "" {
			recipients = append(recipients, alert)
		}
	}
	if len(recipients) == 0 {
		s.opts.Logger.Warn("No email recipients for help request")
		return nil
	}

	subjectFolder := ""
	if config.IncludeFolderInSubject {
		subjectFolder = folder
	}
	for _, recipient := range unique(recipients) {
		already, err := s.opts.Mail.HasEmailNotificationSent(ctx, eventType, requestID, recipient)
		if err != nil {
			return err
		}
		if already {
			continue
		}
		sent, err := s.opts.Mail.SendHelpRequestEmail(ctx, mail.HelpRequestEmail{
			To: []string{recipient},
			HelpRequest: mail.HelpRequest{
				Hostname: jsbind.String(hostname),
				DeviceID: jsbind.String(deviceID),
				Message:  jsbind.String(message),
			},
			FolderName: jsbind.String(subjectFolder),
		})
		if err != nil {
			return err
		}
		if sent {
			if err := s.opts.Mail.LogEmailNotificationSent(ctx, eventType, requestID, recipient); err != nil {
				return err
			}
			s.opts.Logger.Info("Sent help request notification")
		}
	}
	return nil
}

// firstText is the first of the event's members that is a text that is
// not empty, "" when there is none. The server sends every member as
// text.
func firstText(data any, keys ...string) string {
	o, _ := data.(*goapi.Object)
	for _, key := range keys {
		if text, ok := o.Get(key).(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func orText(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// unique is the trimmed addresses without empty ones and repeats, in the
// order they came.
func unique(addresses []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, address := range addresses {
		address = jsbind.Trim(address)
		if address == "" || seen[address] {
			continue
		}
		seen[address] = true
		out = append(out, address)
	}
	return out
}

var (
	apiKeyParam  = regexp.MustCompile(`(?i)api_key=[^&\s]+`)
	apiKeyHeader = regexp.MustCompile(`(?i)(X-API-Key\s*[:=]\s*)\S+`)
)

// sanitizeLogMessage keeps API keys out of a log line.
func sanitizeLogMessage(text string) string {
	text = apiKeyParam.ReplaceAllString(text, "api_key=[REDACTED]")
	return apiKeyHeader.ReplaceAllString(text, "${1}[REDACTED]")
}
