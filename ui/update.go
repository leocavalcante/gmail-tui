package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rdx40/gmail-tui/api"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
)

// Update handles all messages. Never performs blocking work.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		return m.handleResize(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)

	// --- async results ---

	case inboxLoadedMsg:
		m.emailList.SetItems(emailsToItems(msg.emails))
		m.state = stateInbox
		return m, nil

	case emailOpenedMsg:
		m.currentEmail = msg.email
		m.state = stateViewing
		m.viewport.Width = m.width
		m.viewport.Height = m.height - 7
		body := renderMarkdown(msg.email.Body, m.width)
		m.viewport.SetContent(body)
		m.viewport.GotoTop()
		return m, nil

	case emailSentMsg:
		m.state = stateLoading
		m.statusMessage = "Email sent!"
		return m, tea.Batch(m.spinner.Tick, fetchInbox(m.client), clearStatusAfter(3*time.Second))

	case emailDeletedMsg:
		items := m.emailList.Items()
		for i, item := range items {
			if ei, ok := item.(emailItem); ok && ei.email.ID == msg.id {
				m.emailList.RemoveItem(i)
				break
			}
		}
		if m.state == stateViewing {
			m.state = stateInbox
		}
		m.statusMessage = "Email moved to trash"
		return m, clearStatusAfter(3 * time.Second)

	case readToggledMsg:
		items := m.emailList.Items()
		for i, item := range items {
			if ei, ok := item.(emailItem); ok && ei.email.ID == msg.id {
				ei.email.IsUnread = msg.isUnread
				m.emailList.SetItem(i, ei)
				break
			}
		}
		action := "marked as read"
		if msg.isUnread {
			action = "marked as unread"
		}
		m.statusMessage = "Email " + action
		return m, clearStatusAfter(3 * time.Second)

	case labelsLoadedMsg:
		m.labelList.SetItems(labelsToItems(msg.labels))
		m.state = stateLabels
		return m, nil

	case searchResultMsg:
		m.emailList.SetItems(emailsToItems(msg.emails))
		m.state = stateInbox
		return m, nil

	case attachmentSavedMsg:
		m.statusMessage = fmt.Sprintf("Downloaded: %s", msg.path)
		return m, clearStatusAfter(3 * time.Second)

	case errMsg:
		m.statusMessage = "Error: " + msg.err.Error()
		if m.state == stateLoading {
			m.state = stateInbox
		}
		return m, clearStatusAfter(5 * time.Second)

	case statusMsg:
		m.statusMessage = msg.message
		return m, nil

	case clearStatusMsg:
		m.statusMessage = ""
		return m, nil
	}

	return m.updateSubComponents(msg)
}

func (m Model) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.help.Width = msg.Width

	switch m.state {
	case stateInbox:
		m.emailList.SetSize(msg.Width, msg.Height-3)
	case stateViewing:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 7
	case stateLabels:
		m.labelList.SetSize(msg.Width, msg.Height-3)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.showHelp && key.Matches(msg, keys.ShowHelp) {
		m.showHelp = true
		return m, nil
	}
	if m.showHelp && key.Matches(msg, keys.CloseHelp) {
		m.showHelp = false
		return m, nil
	}

	if m.confirmDelete {
		return m.handleDeleteConfirm(msg)
	}

	if m.state == stateViewing && m.downloading {
		return m.handleAttachmentPick(msg)
	}

	switch m.state {
	case stateInbox:
		return m.updateInbox(msg)
	case stateViewing:
		return m.updateViewing(msg)
	case stateComposing:
		return m.updateComposing(msg)
	case stateReplying:
		return m.updateReplying(msg)
	case stateSearching:
		return m.updateSearching(msg)
	case stateLabels:
		return m.updateLabels(msg)
	}
	return m, nil
}

// ---------- state handlers ----------

func (m Model) updateInbox(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Compose):
		m.state = stateComposing
		m.isReply = false
		m.compose.reset("me")
		return m, m.compose.focusField()

	case key.Matches(msg, keys.Search):
		m.state = stateSearching
		m.searchInput.SetValue("")
		m.searchInput.Focus()
		return m, nil

	case key.Matches(msg, keys.Labels):
		m.state = stateLoading
		return m, tea.Batch(m.spinner.Tick, fetchLabelsCmd(m.client))

	case key.Matches(msg, keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, keys.Select):
		if ei, ok := m.emailList.SelectedItem().(emailItem); ok {
			m.currentEmail = &ei.email
			m.state = stateLoading
			return m, tea.Batch(m.spinner.Tick, openEmail(m.client, ei.email.ID))
		}

	case key.Matches(msg, keys.Delete):
		if ei, ok := m.emailList.SelectedItem().(emailItem); ok {
			m.confirmDelete = true
			m.deleteTargetID = ei.email.ID
			m.deleteSubject = ei.email.Subject
			m.statusMessage = fmt.Sprintf("Delete \"%s\"? [y/n]", truncate(ei.email.Subject, 40))
			return m, nil
		}

	case key.Matches(msg, keys.ToggleRead):
		if ei, ok := m.emailList.SelectedItem().(emailItem); ok {
			return m, toggleReadCmd(m.client, ei.email.ID, ei.email.IsUnread)
		}
	}

	var cmd tea.Cmd
	m.emailList, cmd = m.emailList.Update(msg)
	return m, cmd
}

func (m Model) updateViewing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.state = stateInbox
		m.viewport.GotoTop()
		return m, nil

	case key.Matches(msg, keys.Reply):
		m.state = stateReplying
		m.replyTo = m.currentEmail
		m.replyBody.Reset()
		m.replyAttachments = nil
		m.addingAttach = false
		m.replyBody.Focus()
		return m, nil

	case key.Matches(msg, keys.Delete):
		m.confirmDelete = true
		m.deleteTargetID = m.currentEmail.ID
		m.deleteSubject = m.currentEmail.Subject
		m.statusMessage = fmt.Sprintf("Delete \"%s\"? [y/n]", truncate(m.currentEmail.Subject, 40))
		return m, nil

	case key.Matches(msg, keys.ToggleRead):
		return m, toggleReadCmd(m.client, m.currentEmail.ID, m.currentEmail.IsUnread)

	case key.Matches(msg, keys.Labels):
		m.state = stateLoading
		return m, tea.Batch(m.spinner.Tick, fetchLabelsCmd(m.client))

	case key.Matches(msg, keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, keys.DownloadAttachment):
		if m.currentEmail == nil || len(m.currentEmail.Attachments) == 0 {
			return m, notify("No attachments available")
		}
		m.downloading = true
		m.statusMessage = fmt.Sprintf("Select attachment (1-%d) [esc] cancel", len(m.currentEmail.Attachments))
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) updateComposing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEsc:
		if m.compose.addingAttach {
			m.compose.addingAttach = false
			m.compose.attachInput.Reset()
			return m, m.compose.focusField()
		}
		m.state = stateInbox
		return m, nil

	case key.Matches(msg, keys.Send):
		return m, sendEmailCmd(
			m.client,
			m.compose.to.Value(),
			m.compose.cc.Value(),
			m.compose.bcc.Value(),
			m.compose.subject.Value(),
			m.compose.body.Value(),
			m.compose.attachments,
		)

	case key.Matches(msg, keys.AddAttachment):
		if !m.compose.addingAttach {
			m.compose.addingAttach = true
			m.compose.attachInput.SetValue("")
			m.compose.attachInput.Focus()
		}
		return m, nil

	case key.Matches(msg, keys.RemoveAttachment):
		if !m.compose.addingAttach && len(m.compose.attachments) > 0 {
			m.compose.attachments = m.compose.attachments[:len(m.compose.attachments)-1]
			return m, notify("Removed last attachment")
		}

	case msg.Type == tea.KeyEnter && m.compose.addingAttach:
		return m.handleComposeAttach()

	case key.Matches(msg, keys.NextInput):
		if !m.compose.addingAttach {
			m.compose.focused = (m.compose.focused + 1) % 6
			return m, m.compose.focusField()
		}

	case key.Matches(msg, keys.PrevInput):
		if !m.compose.addingAttach {
			m.compose.focused = (m.compose.focused - 1 + 6) % 6
			return m, m.compose.focusField()
		}
	}

	return m.updateComposeInputs(msg)
}

func (m Model) updateReplying(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEsc:
		if m.addingAttach {
			m.addingAttach = false
			m.attachInput.Reset()
			return m, nil
		}
		m.state = stateViewing
		return m, nil

	case key.Matches(msg, keys.Send):
		quoted := fmt.Sprintf(
			"\n\n--- Original Message ---\nFrom: %s\nDate: %s\n\n%s",
			m.replyTo.From, m.replyTo.Date,
			api.IndentText(m.currentEmail.Body),
		)
		subject := m.replyTo.Subject
		if !strings.HasPrefix(strings.ToLower(subject), "re:") {
			subject = "Re: " + subject
		}
		return m, sendEmailCmd(
			m.client,
			m.replyTo.From, "", "",
			subject,
			m.replyBody.Value()+quoted,
			m.replyAttachments,
		)

	case key.Matches(msg, keys.AddAttachment):
		m.addingAttach = true
		m.attachInput.SetValue("")
		m.attachInput.Focus()
		return m, nil

	case key.Matches(msg, keys.RemoveAttachment):
		if len(m.replyAttachments) > 0 {
			m.replyAttachments = m.replyAttachments[:len(m.replyAttachments)-1]
			return m, notify("Removed last attachment")
		}

	case msg.Type == tea.KeyEnter && m.addingAttach:
		path := strings.TrimSpace(m.attachInput.Value())
		if path == "" {
			return m, nil
		}
		if _, err := os.Stat(path); err == nil {
			m.replyAttachments = append(m.replyAttachments, path)
			m.addingAttach = false
			m.attachInput.Reset()
			return m, notify(fmt.Sprintf("Added: %s", filepath.Base(path)))
		}
		return m, notify(fmt.Sprintf("File not found: %s", path))
	}

	var cmd tea.Cmd
	if m.addingAttach {
		m.attachInput, cmd = m.attachInput.Update(msg)
	} else {
		m.replyBody, cmd = m.replyBody.Update(msg)
	}
	return m, cmd
}

func (m Model) updateSearching(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEsc:
		m.state = stateInbox
		return m, nil

	case msg.Type == tea.KeyEnter:
		q := strings.TrimSpace(m.searchInput.Value())
		if q == "" {
			return m, nil
		}
		m.state = stateLoading
		return m, tea.Batch(m.spinner.Tick, searchCmd(m.client, q))
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	return m, cmd
}

func (m Model) updateLabels(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.state = stateInbox
		return m, nil

	case key.Matches(msg, keys.Select):
		if li, ok := m.labelList.SelectedItem().(labelItem); ok {
			m.state = stateLoading
			return m, tea.Batch(m.spinner.Tick, fetchByLabelCmd(m.client, li.label.ID))
		}
	}

	var cmd tea.Cmd
	m.labelList, cmd = m.labelList.Update(msg)
	return m, cmd
}

// ---------- sub-handlers ----------

func (m Model) handleDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.confirmDelete = false
		id := m.deleteTargetID
		m.deleteTargetID = ""
		return m, deleteEmailCmd(m.client, id)
	default:
		m.confirmDelete = false
		m.deleteTargetID = ""
		m.statusMessage = "Delete cancelled"
		return m, clearStatusAfter(2 * time.Second)
	}
}

func (m Model) handleAttachmentPick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc {
		m.downloading = false
		m.statusMessage = ""
		return m, nil
	}
	if msg.Type == tea.KeyRunes {
		digit, err := strconv.Atoi(string(msg.Runes))
		if err == nil && digit > 0 && digit <= len(m.currentEmail.Attachments) {
			m.downloading = false
			att := m.currentEmail.Attachments[digit-1]
			m.statusMessage = fmt.Sprintf("Downloading %s...", att.Filename)
			return m, downloadAttachmentCmd(m.client, m.currentEmail.ID, att)
		}
	}
	return m, nil
}

func (m Model) handleComposeAttach() (tea.Model, tea.Cmd) {
	path := strings.TrimSpace(m.compose.attachInput.Value())
	if path == "" {
		return m, nil
	}
	if _, err := os.Stat(path); err == nil {
		m.compose.attachments = append(m.compose.attachments, path)
		m.compose.addingAttach = false
		m.compose.attachInput.Reset()
		return m, tea.Batch(
			m.compose.focusField(),
			notify(fmt.Sprintf("Added: %s", filepath.Base(path))),
		)
	}
	return m, notify(fmt.Sprintf("File not found: %s", path))
}

func (m Model) updateComposeInputs(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.compose.addingAttach {
		m.compose.attachInput, cmd = m.compose.attachInput.Update(msg)
		return m, cmd
	}
	switch m.compose.focused {
	case 0:
		m.compose.from, cmd = m.compose.from.Update(msg)
	case 1:
		m.compose.to, cmd = m.compose.to.Update(msg)
	case 2:
		m.compose.cc, cmd = m.compose.cc.Update(msg)
	case 3:
		m.compose.bcc, cmd = m.compose.bcc.Update(msg)
	case 4:
		m.compose.subject, cmd = m.compose.subject.Update(msg)
	case 5:
		m.compose.body, cmd = m.compose.body.Update(msg)
	}
	return m, cmd
}

func (m Model) updateSubComponents(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.state {
	case stateLoading:
		m.spinner, cmd = m.spinner.Update(msg)
	case stateViewing:
		m.viewport, cmd = m.viewport.Update(msg)
	case stateReplying:
		if m.addingAttach {
			m.attachInput, cmd = m.attachInput.Update(msg)
		} else {
			m.replyBody, cmd = m.replyBody.Update(msg)
		}
	case stateSearching:
		m.searchInput, cmd = m.searchInput.Update(msg)
	case stateLabels:
		m.labelList, cmd = m.labelList.Update(msg)
	}
	return m, cmd
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func renderMarkdown(body string, width int) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return body
	}
	rendered, err := r.Render(body)
	if err != nil {
		return body
	}
	return rendered
}
