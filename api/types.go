package api

// Email represents a Gmail message.
type Email struct {
	ID          string
	ThreadID    string
	Subject     string
	From        string
	To          string
	CC          string
	BCC         string
	Date        string
	Snippet     string
	Body        string
	Labels      []string
	IsUnread    bool
	Attachments []Attachment
	FullLoaded  bool // true when body+attachments have been fetched
}

// Attachment represents an email attachment.
type Attachment struct {
	ID       string
	Filename string
	MimeType string
	Size     int64
}

// Label represents a Gmail label.
type Label struct {
	ID   string
	Name string
}
