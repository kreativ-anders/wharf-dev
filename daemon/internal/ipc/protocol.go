// Package ipc is the GUI ↔ daemon transport: newline-delimited JSON over a
// unix domain socket. net.Listen("unix", …) runs unmodified on Windows 10
// 1803+, macOS and Linux, so there is no named-pipe branch
// (dev/architecture.md §3, §4).
package ipc

import "encoding/json"

// Method names. The GUI and the tray menu drive the same set, so that both
// surfaces reflect the same state (tray-actions.feature, "Opening the main
// window").
const (
	MethodPing = "ping"
	// MethodAuth presents the token from the endpoint file. It is required as
	// the first call on a TCP connection and ignored on a unix socket, where
	// file permissions already authorise the caller.
	MethodAuth = "auth"
	// MethodState returns the full snapshot the GUI renders.
	MethodState = "state.get"
	// MethodShutdown stops the daemon the way a signal would on macOS and
	// Linux. The app asks rather than signals because on Windows a signal is
	// TerminateProcess, which ends the daemon before it can stop its
	// webservers. The reply may be lost as connections close; the process
	// exiting is the answer.
	MethodShutdown = "daemon.shutdown"

	MethodSetWebserver  = "services.setWebserver"
	MethodAddPHPVersion = "services.addPHPVersion"
	MethodSetPHPVersion = "services.setPHPVersion"
	MethodDetectPHP     = "services.detectPHP"
	MethodInstallPHP    = "services.installPHP"
	// MethodRemovePHP deletes a PHP version Wharf downloaded, or hides one
	// found on the machine; MethodUnhidePHP shows a hidden folder again.
	MethodRemovePHP = "services.removePHP"
	MethodUnhidePHP = "services.unhidePHP"
	// MethodSetPHPTerminal puts Wharf's PHP on the terminal PATH, or takes it
	// off ({"on": bool}).
	MethodSetPHPTerminal = "services.setPHPTerminal"
	MethodSetupSSL       = "services.setupSSL"
	// MethodPHPSettings creates config/php.ini if needed and returns its
	// path, for the GUI to open.
	MethodPHPSettings = "services.phpSettings"
	// MethodPHPReleases looks up the release each PHP download would fetch.
	MethodPHPReleases = "services.phpReleases"
	// MethodInstallWebserver installs nginx or Apache; MethodDetectWebservers
	// re-scans for them.
	MethodInstallWebserver = "services.installWebserver"
	MethodDetectWebservers = "services.detectWebservers"
	MethodSetAppearance    = "settings.setAppearance"
	MethodReset            = "settings.reset"
	MethodStopAll          = "services.stopAll"

	MethodProjectAdd      = "projects.add"
	MethodProjectRemove   = "projects.remove"
	MethodProjectStart    = "projects.start"
	MethodProjectStop     = "projects.stop"
	MethodProjectRestart  = "projects.restart"
	MethodProjectSettings = "projects.settings"
	MethodProjectScaffold = "projects.scaffold"

	// A project's custom config, for the webserver serving it
	// (app-configuration.feature). Read answers with its rules — or, while it
	// has none, its config template's to start from; Save and Delete with the
	// snapshot.
	MethodCustomConfigRead   = "projects.customConfig.read"
	MethodCustomConfigSave   = "projects.customConfig.save"
	MethodCustomConfigDelete = "projects.customConfig.delete"

	MethodTemplates = "templates.list"

	// Config templates (config-templates.feature). Read answers with the
	// rules for one webserver, Create with the new template; Save and Delete
	// — which restores a built-in one — with the snapshot.
	MethodConfigTemplateRead   = "configTemplates.read"
	MethodConfigTemplateSave   = "configTemplates.save"
	MethodConfigTemplateCreate = "configTemplates.create"
	MethodConfigTemplateDelete = "configTemplates.delete"
)

// Event names pushed from daemon to every connected client.
const (
	// EventState carries a full state snapshot. Sending the whole snapshot
	// rather than a delta keeps the GUI free of merge logic, and the payload
	// is a few hundred bytes for a realistic number of projects.
	EventState = "state"
)

// Error codes the GUI branches on. Everything else is shown as a message.
const (
	CodeUnknownMethod   = "unknown_method"
	CodeBadRequest      = "bad_request"
	CodeNotFound        = "not_found"
	CodeConflict        = "conflict"
	CodeElevationDenied = "elevation_denied"
	CodeMissingBinary   = "missing_binary"
	CodeOffline         = "offline"
	CodeInternal        = "internal"
	CodeUnauthorized    = "unauthorized"
)

// Request is one call from a client.
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is the reply to exactly one Request.
type Response struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Event is an unsolicited push. It carries no ID, which is how a client tells
// it apart from a Response.
type Event struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Error is a machine-readable failure.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// frame is the wire shape: a response and an event are distinguished by which
// fields are present, so one decoder handles both.
type frame struct {
	ID     string          `json:"id,omitempty"`
	OK     *bool           `json:"ok,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
	Event  string          `json:"event,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}
