package ui

import (
	"fmt"
	"time"

	"github.com/rdx40/gmail-tui/api"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	inboxQuery     = "in:inbox category:primary"
	defaultMaxResults = 10
	searchMaxResults  = 30
)

// ---------- message types ----------

type inboxLoadedMsg struct{ emails []api.Email }
type emailOpenedMsg struct{ email *api.Email }
type emailSentMsg struct{}
type emailDeletedMsg struct{ id string }
type emailArchivedMsg struct{ id string }
type readToggledMsg struct {
	id       string
	isUnread bool
}
type labelsLoadedMsg struct{ labels []api.Label }
type searchResultMsg struct{ emails []api.Email }
type attachmentSavedMsg struct{ path string }
type errMsg struct{ err error }
type statusMsg struct{ message string }
type clearStatusMsg struct{}

// ---------- commands ----------

func fetchInbox(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		emails, err := client.FetchInbox(inboxQuery, defaultMaxResults)
		if err != nil {
			return errMsg{err: err}
		}
		return inboxLoadedMsg{emails: emails}
	}
}

func openEmail(client *api.Client, id string) tea.Cmd {
	return func() tea.Msg {
		email, err := client.FetchEmail(id)
		if err != nil {
			return errMsg{err: err}
		}
		return emailOpenedMsg{email: email}
	}
}

func sendEmailCmd(client *api.Client, to, cc, bcc, subject, body string, attachments []string) tea.Cmd {
	return func() tea.Msg {
		if err := client.SendEmail(to, cc, bcc, subject, body, attachments); err != nil {
			return errMsg{err: err}
		}
		return emailSentMsg{}
	}
}

func deleteEmailCmd(client *api.Client, id string) tea.Cmd {
	return func() tea.Msg {
		if err := client.DeleteEmail(id); err != nil {
			return errMsg{err: err}
		}
		return emailDeletedMsg{id: id}
	}
}

func archiveEmailCmd(client *api.Client, id string) tea.Cmd {
	return func() tea.Msg {
		if err := client.ArchiveEmail(id); err != nil {
			return errMsg{err: err}
		}
		return emailArchivedMsg{id: id}
	}
}

func toggleReadCmd(client *api.Client, id string, currentlyUnread bool) tea.Cmd {
	return func() tea.Msg {
		newUnread, err := client.ToggleRead(id, currentlyUnread)
		if err != nil {
			return errMsg{err: err}
		}
		return readToggledMsg{id: id, isUnread: newUnread}
	}
}

func searchCmd(client *api.Client, query string) tea.Cmd {
	return func() tea.Msg {
		emails, err := client.Search(query, searchMaxResults)
		if err != nil {
			return errMsg{err: err}
		}
		return searchResultMsg{emails: emails}
	}
}

func fetchByLabelCmd(client *api.Client, labelID string) tea.Cmd {
	return func() tea.Msg {
		emails, err := client.FetchByLabel(labelID, defaultMaxResults)
		if err != nil {
			return errMsg{err: err}
		}
		return searchResultMsg{emails: emails}
	}
}

func fetchLabelsCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		labels, err := client.FetchLabels()
		if err != nil {
			return errMsg{err: err}
		}
		return labelsLoadedMsg{labels: labels}
	}
}

func downloadAttachmentCmd(client *api.Client, msgID string, att api.Attachment) tea.Cmd {
	return func() tea.Msg {
		path, err := client.DownloadAttachment(msgID, att.ID, att.Filename)
		if err != nil {
			return errMsg{err: err}
		}
		return attachmentSavedMsg{path: path}
	}
}

func notify(msg string) tea.Cmd {
	return func() tea.Msg { return statusMsg{message: msg} }
}

func clearStatusAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return clearStatusMsg{} })
}

func notifyTimed(msg string) tea.Cmd {
	return tea.Batch(
		notify(msg),
		clearStatusAfter(3*time.Second),
	)
}

func errNotify(err error) tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return statusMsg{message: fmt.Sprintf("Error: %v", err)} },
		clearStatusAfter(5*time.Second),
	)
}
