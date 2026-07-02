package tools

import (
	"reflect"
	"sort"
	"testing"

	"email-mcp-service/config"

	"github.com/mark3labs/mcp-go/server"
)

func TestRegisterLocalEmailToolsRespectsPermissionsAndOmitsOrganizer(t *testing.T) {
	cfg := &config.Config{
		Permissions: config.PermissionConfig{
			Read:                true,
			Write:               true,
			Send:                true,
			DownloadAttachments: true,
		},
	}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(true))

	RegisterLocalEmailTools(s, cfg)
	names := registeredToolNames(t, s)

	want := []string{
		"applyOrganizePlan",
		"archiveEmails",
		"bulkDeleteEmails",
		"bulkMoveEmails",
		"bulkSetEmailReadStatus",
		"createFolder",
		"deleteEmail",
		"downloadEmailAttachments",
		"getEmail",
		"listEmails",
		"listEmailsV2",
		"listFolders",
		"moveEmail",
		"previewOrganizePlan",
		"resolveSpecialFolders",
		"searchAllFolders",
		"searchEmails",
		"sendEmail",
		"setEmailReadStatus",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("unexpected tools\nwant: %#v\n got: %#v", want, names)
	}
	if containsTool(names, "organizeInvoices") {
		t.Fatalf("organizeInvoices should not be registered in local desktop mode")
	}
}

func TestRegisterLocalEmailToolsCanDisableWriteAndSend(t *testing.T) {
	cfg := &config.Config{
		Permissions: config.PermissionConfig{
			Read:                true,
			Write:               false,
			Send:                false,
			DownloadAttachments: true,
		},
	}
	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(true))

	RegisterLocalEmailTools(s, cfg)
	names := registeredToolNames(t, s)

	for _, blocked := range []string{"sendEmail", "setEmailReadStatus", "moveEmail", "deleteEmail", "createFolder", "bulkMoveEmails", "bulkDeleteEmails", "bulkSetEmailReadStatus", "archiveEmails", "applyOrganizePlan"} {
		if containsTool(names, blocked) {
			t.Fatalf("tool %s should not be registered when permission is disabled: %#v", blocked, names)
		}
	}
	for _, allowed := range []string{"listFolders", "listEmails", "listEmailsV2", "getEmail", "downloadEmailAttachments", "resolveSpecialFolders", "searchAllFolders", "searchEmails", "previewOrganizePlan"} {
		if !containsTool(names, allowed) {
			t.Fatalf("tool %s should be registered when read/download permissions are enabled: %#v", allowed, names)
		}
	}
}

func registeredToolNames(t *testing.T, s *server.MCPServer) []string {
	t.Helper()
	value := reflect.ValueOf(s).Elem().FieldByName("tools")
	if !value.IsValid() {
		t.Fatalf("mcp server tools field not found")
	}
	names := make([]string, 0, value.Len())
	for _, key := range value.MapKeys() {
		names = append(names, key.String())
	}
	sort.Strings(names)
	return names
}

func containsTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
