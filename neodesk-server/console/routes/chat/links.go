package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/unitronix/betterdesk-server/console/session"
)

// A chat file's links say who may download it: the account that uploaded
// it, and the conversations it was shared into. The upload names no
// conversation, and the API server keeps no trace of a shared file (a
// file_share frame is passed on, not stored), so the console notes both
// itself, in <files folder>/.links/<file id>, one JSON object a line:
// {"owner":<account id>} from the upload, {"conversation":"<id>"} for
// every share.
const linksFolder = ".links"

// link is one line of a file's links.
type link struct {
	Owner        int64  `json:"owner,omitempty"`
	Conversation string `json:"conversation,omitempty"`
}

// fileLinks are the links of one file.
type fileLinks struct {
	owner         int64
	conversations []string
}

// readLinks reads the links of file id; a file without links has none.
func readLinks(dir, id string) (fileLinks, error) {
	var out fileLinks
	f, err := os.Open(filepath.Join(dir, linksFolder, id))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("read the links of chat file %s: %w", id, err)
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		var l link
		if json.Unmarshal(lines.Bytes(), &l) != nil {
			continue
		}
		if l.Owner != 0 && out.owner == 0 {
			out.owner = l.Owner
		}
		if l.Conversation != "" {
			out.conversations = append(out.conversations, l.Conversation)
		}
	}
	if err := lines.Err(); err != nil {
		return out, fmt.Errorf("read the links of chat file %s: %w", id, err)
	}
	return out, nil
}

// addLink appends a line to the links of file id.
func addLink(dir, id string, l link) error {
	line, err := json.Marshal(l)
	if err != nil {
		return err
	}
	folder := filepath.Join(dir, linksFolder)
	if err := os.MkdirAll(folder, 0o777); err != nil {
		return fmt.Errorf("make the links folder of the chat files: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(folder, id), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
	if err != nil {
		return fmt.Errorf("open the links of chat file %s: %w", id, err)
	}
	_, err = f.Write(append(line, '\n'))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write the links of chat file %s: %w", id, err)
	}
	return nil
}

// findFile is the name of the stored file of id: the id itself or the id
// and an extension; "" when there is none.
func findFile(dir, id string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	for _, entry := range entries {
		if entry.Name() == id || strings.HasPrefix(entry.Name(), id+".") {
			return entry.Name(), nil
		}
	}
	return "", nil
}

// mayDownload reports whether the account may download file id: it
// uploaded the file, it sees a conversation the file was shared into, or
// it sees every device (the only way to a file without links, such as
// one uploaded before links were kept).
func mayDownload(ctx context.Context, dir string, scope Scope, user *session.User, id string) (bool, error) {
	if user == nil {
		return false, nil
	}
	links, err := readLinks(dir, id)
	if err != nil {
		return false, err
	}
	if links.owner != 0 && links.owner == user.ID {
		return true, nil
	}
	if scope.unlimited(ctx, user) {
		return true, nil
	}
	for _, conversation := range links.conversations {
		if scope.sees(ctx, user, conversation) {
			return true, nil
		}
	}
	return false, nil
}

// shareFile notes that file id was shared into a conversation, when the
// account that shared it may download it and the file is there. A share
// of anything else changes nothing.
func shareFile(ctx context.Context, dir string, scope Scope, user *session.User, id, conversation string) error {
	if dir == "" || !isFileID(id) || conversation == "" {
		return nil
	}
	name, err := findFile(dir, id)
	if err != nil || name == "" {
		return err
	}
	allowed, err := mayDownload(ctx, dir, scope, user, id)
	if err != nil || !allowed {
		return err
	}
	links, err := readLinks(dir, id)
	if err != nil {
		return err
	}
	for _, known := range links.conversations {
		if known == conversation {
			return nil
		}
	}
	return addLink(dir, id, link{Conversation: conversation})
}
