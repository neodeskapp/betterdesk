package scheduledbackup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/backup"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// SMB is a share as validateSmbDetails left it.
type SMB struct {
	Host, Share, Domain, Username, Password, MountPath string
}

var (
	smbHost  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	smbShare = regexp.MustCompile(`^[A-Za-z0-9$._-]+$`)
)

// ValidateSMB is validateSmbDetails: host and share trimmed and limited
// to plain characters, domain and user name trimmed and cut to 128
// characters, the password cut to 512, the mount path trimmed and cut to
// 1024.
func ValidateSMB(v any) (SMB, error) {
	field := func(key string, trim bool, limit int) (string, error) {
		s, err := textOr(prop(v, key))
		if err != nil {
			return "", err
		}
		if trim {
			s = jsbind.Trim(s)
		}
		if limit > 0 {
			s = utf16Slice(s, limit)
		}
		return s, nil
	}
	var smb SMB
	var err error
	if smb.Host, err = field("host", true, 0); err != nil {
		return SMB{}, err
	}
	if smb.Share, err = field("share", true, 0); err != nil {
		return SMB{}, err
	}
	if !smbHost.MatchString(smb.Host) {
		return SMB{}, errors.New("SMB host contains invalid characters")
	}
	if !smbShare.MatchString(smb.Share) {
		return SMB{}, errors.New("SMB share contains invalid characters")
	}
	if smb.Domain, err = field("domain", true, 128); err != nil {
		return SMB{}, err
	}
	if smb.Username, err = field("username", true, 128); err != nil {
		return SMB{}, err
	}
	if smb.Password, err = field("password", false, 512); err != nil {
		return SMB{}, err
	}
	if smb.MountPath, err = field("mountPath", true, 1024); err != nil {
		return SMB{}, err
	}
	return smb, nil
}

// object is the share as it is stored, the password in clear.
func (m SMB) object() *branding.Object {
	o := branding.NewObject()
	o.Set("host", m.Host)
	o.Set("share", m.Share)
	o.Set("domain", m.Domain)
	o.Set("username", m.Username)
	o.Set("password", m.Password)
	o.Set("mountPath", m.MountPath)
	return o
}

// masked is v with its password replaced by the mask, or by "" when there
// is none: {...v, password: v.password ? "********" : ""}.
func masked(v any) *branding.Object {
	o := spread(v)
	password, _ := o.Get("password")
	if branding.Truthy(password) {
		o.Set("password", PasswordMask)
	} else {
		o.Set("password", "")
	}
	return o
}

// GetSMB returns the stored share with the password masked, as JSON.
func (s *Service) GetSMB(ctx context.Context) (string, error) {
	stored, err := s.getSetting(ctx, SMBKey)
	if err != nil {
		return "", err
	}
	return branding.Stringify(masked(parseStored(stored, branding.NewObject()))), nil
}

// SaveSMB checks and stores a share and returns it masked, as JSON. A
// password equal to the mask keeps the stored one.
func (s *Service) SaveSMB(ctx context.Context, v any) (string, error) {
	stored, err := s.getSetting(ctx, SMBKey)
	if err != nil {
		return "", err
	}
	current := parseStored(stored, branding.NewObject())
	input, err := ValidateSMB(v)
	if err != nil {
		return "", err
	}
	obj := input.object()
	if input.Password == PasswordMask {
		var keep any = ""
		if p := prop(current, "password"); branding.Truthy(p) {
			keep = p
		}
		obj.Set("password", keep)
	}
	if err := s.setSetting(ctx, SMBKey, branding.Stringify(obj)); err != nil {
		return "", err
	}
	return branding.Stringify(masked(obj)), nil
}

// TestSMB is the route's test: it saves the share like SaveSMB, then
// tests it with the stored values over the ones sent when a password is
// stored (the mask came back), and with the ones sent otherwise.
func (s *Service) TestSMB(ctx context.Context, body any) (string, error) {
	saved, err := s.SaveSMB(ctx, body)
	if err != nil {
		return "", err
	}
	raw := spread(body)
	savedObj, _ := branding.ParseJSON([]byte(saved))
	if prop(savedObj, "password") == PasswordMask {
		stored, err := s.getSetting(ctx, SMBKey)
		if err != nil {
			return "", err
		}
		text := "{}"
		if stored.Valid && stored.String != "" {
			text = stored.String
		}
		if parsed, err := branding.ParseJSON([]byte(text)); err == nil {
			// Object.assign(raw, parsed)
			for _, k := range spread(parsed).Keys() {
				value, _ := spread(parsed).Get(k)
				raw.Set(k, value)
			}
		}
	}
	return s.TestSMBConnection(ctx, raw)
}

// TestSMBConnection lists the share with smbclient (on Windows: net use
// and net use /delete). It returns the share as it was addressed. The
// password never goes on the command line, where every local user could
// read it: smbclient gets it in PASSWD, net use on stdin.
func (s *Service) TestSMBConnection(ctx context.Context, v any) (string, error) {
	smb, err := ValidateSMB(v)
	if err != nil {
		return "", err
	}
	if *s.o.Windows {
		target := `\\` + smb.Host + `\` + smb.Share
		user := smb.Username
		if smb.Domain != "" {
			user = smb.Domain + `\` + smb.Username
		}
		c := Command{Name: "net", Args: []string{"use", target}}
		if user != "" {
			// "*" makes net use ask for the password, which it reads from
			// stdin.
			c.Args = append(c.Args, "*", "/user:"+argText(user))
			c.Stdin = argText(smb.Password) + "\r\n"
		}
		defer func() {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.o.SMBCleanupTimeout)
			defer cancel()
			_, _ = s.o.Run(cctx, Command{Name: "net", Args: []string{"use", target, "/delete", "/y"}})
		}()
		if err := s.runSMB(ctx, c); err != nil {
			return "", err
		}
		return target, nil
	}
	target := "//" + smb.Host + "/" + smb.Share
	c := Command{Name: "smbclient", Args: []string{target}}
	if smb.Username != "" {
		user := smb.Username
		if smb.Domain != "" {
			user = smb.Domain + `\` + user
		}
		// smbclient takes the password of a user given without
		// "%password" from the variable PASSWD.
		c.Args = append(c.Args, "-U", argText(user))
		c.Env = []string{"PASSWD=" + argText(smb.Password)}
	} else {
		c.Args = append(c.Args, "-N")
	}
	c.Args = append(c.Args, "-c", "dir")
	if err := s.runSMB(ctx, c); err != nil {
		if errors.Is(err, errNotInstalled) {
			return "", errors.New("smbclient is not installed; mount the share in the operating system and test its path instead")
		}
		return "", err
	}
	return target, nil
}

var errNotInstalled = errors.New("not installed")

// runSMB runs a program with the test's time limit and gives its failure
// the old messages.
func (s *Service) runSMB(ctx context.Context, c Command) error {
	name := c.Name
	rctx, cancel := context.WithTimeout(ctx, s.o.SMBTimeout)
	defer cancel()
	code, err := s.o.Run(rctx, c)
	switch {
	case rctx.Err() != nil && ctx.Err() == nil:
		return errors.New("SMB connection test timed out")
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist):
		if name == "smbclient" {
			return errNotInstalled
		}
		return errors.New("spawn " + name + " ENOENT")
	case err != nil:
		if code, ok := backup.ErrnoCode(err); ok {
			return errors.New("spawn " + name + " " + code)
		}
		return err
	case code != 0:
		return errors.New("SMB command failed with exit code " + strconv.Itoa(code))
	}
	return nil
}

// argText is a string as Node passed it to a program: a lone surrogate
// becomes U+FFFD.
func argText(s string) string { return strings.ToValidUTF8(s, "\ufffd") }

// runProgram runs a program and returns its exit status. Its output is
// read and dropped.
func runProgram(ctx context.Context, c Command) (int, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, err
	}
	return 0, nil
}
