// Package credentials stores mailbox authorization codes in the OS credential store.
package credentials

const (
	SMTPKind = "smtp"
	IMAPKind = "imap"
)

// Store reads and writes local mailbox secrets.
type Store struct{}

func target(kind string) string {
	return "EmailMCP/default/" + kind
}
