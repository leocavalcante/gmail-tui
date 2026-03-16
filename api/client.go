package api

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"google.golang.org/api/gmail/v1"
)

const (
	maxConcurrent     = 5
	maxAttachmentSize = 25 * 1024 * 1024
	downloadsDir      = "downloads"
)

// Client wraps the Gmail API with caching and concurrent fetching.
type Client struct {
	srv   *gmail.Service
	cache sync.Map // map[string]*Email
}

// NewClient authenticates and returns a ready-to-use Client.
func NewClient() (*Client, error) {
	srv, err := newGmailService()
	if err != nil {
		return nil, err
	}
	return &Client{srv: srv}, nil
}

// ---------- read operations ----------

// FetchInbox returns emails from the primary inbox, fetched concurrently.
func (c *Client) FetchInbox(query string, max int64) ([]Email, error) {
	return c.fetchList(query, "", max)
}

// Search returns emails matching query, fetched concurrently.
func (c *Client) Search(query string, max int64) ([]Email, error) {
	return c.fetchList(query, "", max)
}

// FetchByLabel returns emails with the given label, fetched concurrently.
func (c *Client) FetchByLabel(labelID string, max int64) ([]Email, error) {
	return c.fetchList("", labelID, max)
}

// FetchEmail returns a fully-loaded email (body + attachments).
// Returns from cache if already fully loaded.
func (c *Client) FetchEmail(id string) (*Email, error) {
	if v, ok := c.cache.Load(id); ok {
		if e := v.(*Email); e.FullLoaded {
			return e, nil
		}
	}

	msg, err := c.srv.Users.Messages.Get("me", id).Format("full").Do()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch message: %w", err)
	}

	email := parseMessage(msg, true)
	c.cacheStore(email)
	return email, nil
}

// FetchLabels returns all Gmail labels.
func (c *Client) FetchLabels() ([]Label, error) {
	resp, err := c.srv.Users.Labels.List("me").Do()
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return []Label{}, nil
	}
	out := make([]Label, len(resp.Labels))
	for i, l := range resp.Labels {
		out[i] = Label{ID: l.Id, Name: l.Name}
	}
	return out, nil
}

// ---------- write operations ----------

// DeleteEmail moves an email to trash and removes it from cache.
func (c *Client) DeleteEmail(id string) error {
	_, err := c.srv.Users.Messages.Trash("me", id).Do()
	if err == nil {
		c.cache.Delete(id)
	}
	return err
}

// ToggleRead flips the UNREAD label. Returns the new isUnread state.
func (c *Client) ToggleRead(id string, currentlyUnread bool) (newUnread bool, err error) {
	mod := &gmail.ModifyMessageRequest{}
	if currentlyUnread {
		mod.RemoveLabelIds = []string{"UNREAD"}
	} else {
		mod.AddLabelIds = []string{"UNREAD"}
	}

	_, err = c.srv.Users.Messages.Modify("me", id, mod).Do()
	if err != nil {
		return currentlyUnread, err
	}

	newUnread = !currentlyUnread
	if v, ok := c.cache.Load(id); ok {
		e := v.(*Email)
		e.IsUnread = newUnread
	}
	return newUnread, nil
}

// SendEmail constructs a proper MIME message and sends it.
func (c *Client) SendEmail(to, cc, bcc, subject, body string, attachments []string) error {
	var mimeBody bytes.Buffer
	writer := multipart.NewWriter(&mimeBody)

	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	textPart, err := writer.CreatePart(textHeader)
	if err != nil {
		return fmt.Errorf("failed to create text part: %w", err)
	}
	if _, err := textPart.Write([]byte(body)); err != nil {
		return fmt.Errorf("failed to write body: %w", err)
	}

	for _, fp := range attachments {
		if err := writeAttachment(writer, fp); err != nil {
			return err
		}
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close writer: %w", err)
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	if cc != "" {
		fmt.Fprintf(&msg, "Cc: %s\r\n", cc)
	}
	if bcc != "" {
		fmt.Fprintf(&msg, "Bcc: %s\r\n", bcc)
	}
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/mixed; boundary=%s\r\n\r\n", writer.Boundary())
	msg.Write(mimeBody.Bytes())

	raw := base64.URLEncoding.EncodeToString(msg.Bytes())
	_, err = c.srv.Users.Messages.Send("me", &gmail.Message{Raw: raw}).Do()
	return err
}

// DownloadAttachment saves an attachment to the downloads directory.
func (c *Client) DownloadAttachment(msgID, attachmentID, filename string) (string, error) {
	att, err := c.srv.Users.Messages.Attachments.Get("me", msgID, attachmentID).Do()
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	data, err := base64.RawURLEncoding.DecodeString(att.Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode: %w", err)
	}

	if err := os.MkdirAll(downloadsDir, 0755); err != nil {
		return "", fmt.Errorf("couldn't create downloads directory: %w", err)
	}

	path := filepath.Join(downloadsDir, sanitizeFilename(filename))
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("save failed: %w", err)
	}

	return path, nil
}

// ---------- internal ----------

// fetchList gets message IDs then fetches all concurrently.
func (c *Client) fetchList(query, labelID string, max int64) ([]Email, error) {
	call := c.srv.Users.Messages.List("me").MaxResults(max)
	if query != "" {
		call = call.Q(query)
	}
	if labelID != "" {
		call = call.LabelIds(labelID)
	}

	resp, err := call.Do()
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Messages) == 0 {
		return []Email{}, nil
	}

	return c.fetchConcurrently(resp.Messages), nil
}

// fetchConcurrently fetches metadata for all messages using a bounded worker pool.
// Preserves the original ordering from the API response.
func (c *Client) fetchConcurrently(messages []*gmail.Message) []Email {
	results := make([]*Email, len(messages))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrent)

	for i, m := range messages {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			msg, err := c.srv.Users.Messages.Get("me", id).
				Format("metadata").
				MetadataHeaders("Subject", "From", "Date", "To", "Cc", "Bcc").
				Do()
			if err != nil {
				log.Printf("Error fetching message %s: %v", id, err)
				return
			}
			email := parseMessage(msg, false)
			c.cacheStore(email)
			results[idx] = email
		}(i, m.Id)
	}

	wg.Wait()

	// Compact: drop nil (failed) entries while preserving order.
	emails := make([]Email, 0, len(results))
	for _, e := range results {
		if e != nil {
			emails = append(emails, *e)
		}
	}
	return emails
}

// cacheStore inserts or updates an email in cache.
// Never overwrites a full entry with a metadata-only entry.
func (c *Client) cacheStore(email *Email) {
	if v, ok := c.cache.Load(email.ID); ok {
		existing := v.(*Email)
		if existing.FullLoaded && !email.FullLoaded {
			// Just refresh mutable fields.
			existing.IsUnread = email.IsUnread
			existing.Labels = email.Labels
			return
		}
	}
	cp := *email
	c.cache.Store(email.ID, &cp)
}

func writeAttachment(writer *multipart.Writer, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open attachment: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}
	if info.Size() > maxAttachmentSize {
		return fmt.Errorf("attachment too large: %s (max 25MB)", filepath.Base(filePath))
	}

	partHeader := textproto.MIMEHeader{}
	mimeType := mime.TypeByExtension(filepath.Ext(filePath))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	partHeader.Set("Content-Type", mimeType)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(filePath)))
	partHeader.Set("Content-Transfer-Encoding", "base64")

	pw, err := writer.CreatePart(partHeader)
	if err != nil {
		return fmt.Errorf("failed to create attachment part: %w", err)
	}
	encoder := base64.NewEncoder(base64.StdEncoding, pw)
	if _, err := io.Copy(encoder, file); err != nil {
		return fmt.Errorf("failed to write attachment: %w", err)
	}
	return encoder.Close()
}

func sanitizeFilename(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsLetter(r) || unicode.IsNumber(r) ||
			r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, name)
}
