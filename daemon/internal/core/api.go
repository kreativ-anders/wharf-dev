package core

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/certs"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/elevate"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/php"
	wruntime "github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/shellpath"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/webserver"
)

// The params each method takes, as the GUI and wharfctl send them.
type (
	nameParams struct {
		Name string `json:"name"`
	}
	versionParams struct {
		Version string `json:"version"`
	}
	dirParams struct {
		Dir string `json:"dir"`
	}
	modeParams struct {
		Mode string `json:"mode"`
	}
	onParams struct {
		On bool `json:"on"`
	}
	resetParams struct {
		DeleteProjects bool `json:"delete_projects"`
	}
	// INFO: One struct, not Adding embedded: both carry "name", and the outer
	// one would hide the inner.
	addParams struct {
		Name      string  `json:"name"`
		Path      string  `json:"path"`
		Template  *string `json:"template"`
		Webserver string  `json:"webserver"`
		Start     bool    `json:"start"`
	}
	pathParams struct {
		Path string `json:"path"`
	}
	settingsParams struct {
		Name     string   `json:"name"`
		Settings Settings `json:"settings"`
	}
	customConfigParams struct {
		Name      string `json:"name"`
		Webserver string `json:"webserver"`
		Content   string `json:"content"`
	}
	configTemplateParams struct {
		ID        string `json:"id"`
		Webserver string `json:"webserver"`
		Content   string `json:"content"`
	}
)

// Register wires the daemon's methods onto an IPC server and arranges for
// every state change to be broadcast, so a connected GUI never has to poll.
func (d *Daemon) Register(srv *ipc.Server) {
	d.OnState(func(st State) { srv.Broadcast(ipc.EventState, st) })

	// INFO: A download or an install takes minutes, and the GUI sends every
	// click on one connection: these run beside the other requests, so a
	// click on another project is answered meanwhile (single-application
	// .feature, "A download does not hold up other actions"). Each guards
	// itself against running twice, and none holds mu while it waits.
	background := map[string]bool{
		ipc.MethodInstallPHP:       true,
		ipc.MethodInstallWebserver: true,
		ipc.MethodSetupSSL:         true,
		ipc.MethodPHPReleases:      true,
	}
	handle := func(method string, h ipc.Handler) {
		if background[method] {
			srv.HandleBackground(method, h)
			return
		}
		srv.Handle(method, h)
	}

	handle(ipc.MethodPing, query(func(context.Context) any { return map[string]string{"pong": "wharf"} }))
	handle(ipc.MethodState, query(func(context.Context) any { return d.State() }))
	handle(ipc.MethodDetectPHP, query(func(ctx context.Context) any { return d.RefreshPHP(ctx) }))

	// INFO: An action answers with the snapshot it leaves behind, so the GUI
	// renders its outcome without asking a second time.
	for method, act := range map[string]func(context.Context) error{
		ipc.MethodDetectWebservers: func(ctx context.Context) error { d.RefreshWebservers(ctx); return nil },
		ipc.MethodSetupSSL:         d.SetupSSL,
		ipc.MethodPHPReleases:      d.CheckPHPReleases,
		ipc.MethodStopAll:          d.StopAll,
	} {
		handle(method, d.stateAfter(act))
	}
	for method, act := range map[string]func(context.Context, string) error{
		ipc.MethodSetWebserver:     d.SetWebserver,
		ipc.MethodInstallWebserver: d.InstallWebserver,
		ipc.MethodProjectRemove:    d.RemoveProject,
		ipc.MethodProjectStart:     d.StartProject,
		ipc.MethodProjectStop:      d.StopProject,
		ipc.MethodProjectRestart:   d.RestartProject,
	} {
		handle(method, action(d, func(ctx context.Context, p nameParams) error { return act(ctx, p.Name) }))
	}
	for method, act := range map[string]func(context.Context, string) error{
		ipc.MethodAddPHPVersion: d.AddPHPVersion,
		ipc.MethodSetPHPVersion: d.SetPHPVersion,
		ipc.MethodInstallPHP:    d.InstallPHP,
		ipc.MethodRemovePHP:     d.RemovePHP,
	} {
		handle(method, action(d, func(ctx context.Context, p versionParams) error { return act(ctx, p.Version) }))
	}
	handle(ipc.MethodUnhidePHP, action(d, func(ctx context.Context, p dirParams) error { return d.UnhidePHP(ctx, p.Dir) }))
	handle(ipc.MethodReset, action(d, func(ctx context.Context, p resetParams) error { return d.Reset(ctx, p.DeleteProjects) }))
	handle(ipc.MethodSetPHPTerminal, action(d, func(ctx context.Context, p onParams) error { return d.SetPHPTerminal(ctx, p.On) }))
	handle(ipc.MethodConfigTemplateSave, action(d, func(ctx context.Context, p configTemplateParams) error {
		return d.SaveConfigTemplate(ctx, p.ID, p.Webserver, p.Content)
	}))
	handle(ipc.MethodConfigTemplateDelete, action(d, func(ctx context.Context, p configTemplateParams) error {
		return d.DeleteConfigTemplate(ctx, p.ID)
	}))
	handle(ipc.MethodCustomConfigSave, action(d, func(ctx context.Context, p customConfigParams) error {
		return d.SaveCustomConfig(ctx, p.Name, p.Webserver, p.Content)
	}))
	handle(ipc.MethodCustomConfigDelete, action(d, func(ctx context.Context, p customConfigParams) error {
		return d.DeleteCustomConfig(ctx, p.Name, p.Webserver)
	}))
	handle(ipc.MethodSetAppearance, action(d, func(_ context.Context, p modeParams) error { return d.SetAppearance(p.Mode) }))

	// INFO: These answer with what they made or changed, not the snapshot.
	handle(ipc.MethodPHPSettings, func(ctx context.Context, _ json.RawMessage) (any, error) {
		path, err := d.PHPSettings(ctx)
		if err != nil {
			return nil, asIPCError(err)
		}
		return map[string]string{"path": path}, nil
	})
	handle(ipc.MethodProjectAdd, handler(func(ctx context.Context, p addParams) (any, error) {
		// INFO: A name adds a folder from www/; a path adds a folder from
		// anywhere (project-folders.feature).
		if p.Path != "" {
			return d.AddFolder(ctx, p.Path, Adding{Name: p.Name, Template: p.Template, Webserver: p.Webserver, Start: p.Start})
		}
		return d.AddProject(ctx, p.Name)
	}))
	handle(ipc.MethodProjectSettings, handler(func(ctx context.Context, p settingsParams) (any, error) {
		return d.UpdateSettings(ctx, p.Name, p.Settings)
	}))
	handle(ipc.MethodCustomConfigRead, handler(func(_ context.Context, p customConfigParams) (any, error) {
		return d.ReadCustomConfig(p.Name)
	}))
	handle(ipc.MethodConfigTemplateRead, handler(func(_ context.Context, p configTemplateParams) (any, error) {
		body, err := d.ReadConfigTemplate(p.ID, p.Webserver)
		return map[string]string{"content": body}, err
	}))
	handle(ipc.MethodConfigTemplateCreate, handler(func(ctx context.Context, p nameParams) (any, error) {
		return d.CreateConfigTemplate(ctx, p.Name)
	}))
	handle(ipc.MethodProjectInspect, handler(func(_ context.Context, p pathParams) (any, error) {
		return d.InspectFolder(p.Path)
	}))
}

// query answers a method that takes no params and cannot fail.
func query(fn func(context.Context) any) ipc.Handler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) { return fn(ctx), nil }
}

// stateAfter runs an action that takes no params and answers with the
// snapshot it left behind.
func (d *Daemon) stateAfter(act func(context.Context) error) ipc.Handler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := act(ctx); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	}
}

// action decodes an action's params and answers with the snapshot it left
// behind.
func action[P any](d *Daemon, act func(context.Context, P) error) ipc.Handler {
	return handler(func(ctx context.Context, p P) (any, error) {
		if err := act(ctx, p); err != nil {
			return nil, err
		}
		return d.State(), nil
	})
}

// handler decodes a request's params into P, runs fn, and maps its error onto
// a code the GUI branches on.
func handler[P any](fn func(context.Context, P) (any, error)) ipc.Handler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p P
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		out, err := fn(ctx, p)
		if err != nil {
			return nil, asIPCError(err)
		}
		return out, nil
	}
}

func decode(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return ipc.Errorf(ipc.CodeBadRequest, "missing params")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return ipc.Errorf(ipc.CodeBadRequest, "invalid params: %v", err)
	}
	return nil
}

// asIPCError maps the daemon's failure modes onto codes the GUI branches on,
// so that a declined elevation prompt or a missing binary is presented as a
// state rather than as an error dialog.
func asIPCError(err error) error {
	var (
		missing  *wruntime.MissingBinaryError
		notFound *NotFoundError
		invalid  *InvalidError
		conflict *ConflictError
		broken   *shellpath.BrokenBlockError
	)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, elevate.ErrDeclined):
		return ipc.Errorf(ipc.CodeElevationDenied, "%s", err.Error())
	case errors.Is(err, php.ErrDownload), errors.Is(err, certs.ErrFetch),
		errors.Is(err, webserver.ErrFetch):
		return ipc.Errorf(ipc.CodeOffline, "%s", err.Error())
	case errors.Is(err, certs.ErrMkcertMissing):
		return ipc.Errorf(ipc.CodeMissingBinary, "%s", err.Error())
	case errors.As(err, &missing):
		return ipc.Errorf(ipc.CodeMissingBinary, "%s", err.Error())
	case errors.As(err, &notFound):
		return ipc.Errorf(ipc.CodeNotFound, "%s", err.Error())
	case errors.As(err, &invalid):
		return ipc.Errorf(ipc.CodeBadRequest, "%s", err.Error())
	case errors.As(err, &conflict), errors.As(err, &broken):
		return ipc.Errorf(ipc.CodeConflict, "%s", err.Error())
	}
	return err
}
