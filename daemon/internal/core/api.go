package core

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/manuel-steinberg/wharf/daemon/internal/certs"
	"github.com/manuel-steinberg/wharf/daemon/internal/elevate"
	"github.com/manuel-steinberg/wharf/daemon/internal/ipc"
	"github.com/manuel-steinberg/wharf/daemon/internal/php"
	"github.com/manuel-steinberg/wharf/daemon/internal/project"
	wruntime "github.com/manuel-steinberg/wharf/daemon/internal/runtime"
	"github.com/manuel-steinberg/wharf/daemon/internal/webserver"
)

// Register wires the daemon's methods onto an IPC server and arranges for
// every state change to be broadcast, so a connected GUI never has to poll.
func (d *Daemon) Register(srv *ipc.Server) {
	d.OnState(func(st State) { srv.Broadcast(ipc.EventState, st) })

	srv.Handle(ipc.MethodPing, func(context.Context, json.RawMessage) (any, error) {
		return map[string]string{"pong": "wharf"}, nil
	})
	srv.Handle(ipc.MethodState, func(context.Context, json.RawMessage) (any, error) {
		return d.State(), nil
	})
	srv.Handle(ipc.MethodTemplates, func(context.Context, json.RawMessage) (any, error) {
		return d.Templates(), nil
	})

	srv.Handle(ipc.MethodSetWebserver, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := d.SetWebserver(ctx, p.Name); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodAddPHPVersion, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Version string `json:"version"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := d.AddPHPVersion(ctx, p.Version); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodSetPHPVersion, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Version string `json:"version"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := d.SetPHPVersion(ctx, p.Version); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodDetectPHP, func(ctx context.Context, _ json.RawMessage) (any, error) {
		return d.RefreshPHP(ctx), nil
	})

	srv.Handle(ipc.MethodInstallPHP, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Version string `json:"version"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := d.InstallPHP(ctx, p.Version); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodInstallWebserver, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := d.InstallWebserver(ctx, p.Name); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodDetectWebservers, func(ctx context.Context, _ json.RawMessage) (any, error) {
		d.RefreshWebservers(ctx)
		return d.State(), nil
	})

	srv.Handle(ipc.MethodSetAppearance, func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Mode string `json:"mode"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := d.SetAppearance(p.Mode); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodSetupSSL, func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := d.SetupSSL(ctx); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodStopAll, func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := d.StopAll(ctx); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodProjectAdd, func(ctx context.Context, raw json.RawMessage) (any, error) {
		// A name adds a folder from www/; a path adds a folder from anywhere
		// (project-folders.feature).
		var p struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		var added Project
		var err error
		if p.Path != "" {
			added, err = d.AddFolder(ctx, p.Path)
		} else {
			added, err = d.AddProject(ctx, p.Name)
		}
		if err != nil {
			return nil, asIPCError(err)
		}
		return added, nil
	})

	srv.Handle(ipc.MethodProjectRemove, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := d.RemoveProject(ctx, p.Name); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodProjectStart, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := d.StartProject(ctx, p.Name); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodProjectStop, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := d.StopProject(ctx, p.Name); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodProjectRestart, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		if err := d.RestartProject(ctx, p.Name); err != nil {
			return nil, asIPCError(err)
		}
		return d.State(), nil
	})

	srv.Handle(ipc.MethodProjectSettings, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name     string   `json:"name"`
			Settings Settings `json:"settings"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		updated, err := d.UpdateSettings(ctx, p.Name, p.Settings)
		if err != nil {
			return nil, asIPCError(err)
		}
		return updated, nil
	})

	srv.Handle(ipc.MethodProjectCustomConfig, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Name      string `json:"name"`
			Webserver string `json:"webserver"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		path, err := d.CustomConfig(ctx, p.Name, p.Webserver)
		if err != nil {
			return nil, asIPCError(err)
		}
		return map[string]string{"path": path}, nil
	})

	srv.Handle(ipc.MethodProjectScaffold, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Template string `json:"template"`
			Name     string `json:"name"`
		}
		if err := decode(raw, &p); err != nil {
			return nil, err
		}
		created, err := d.Scaffold(ctx, p.Template, p.Name)
		if err != nil {
			return nil, asIPCError(err)
		}
		return created, nil
	})
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
	)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, elevate.ErrDeclined):
		return ipc.Errorf(ipc.CodeElevationDenied, "%s", err.Error())
	case errors.Is(err, project.ErrOffline), errors.Is(err, php.ErrDownload), errors.Is(err, certs.ErrFetch),
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
	case errors.As(err, &conflict):
		return ipc.Errorf(ipc.CodeConflict, "%s", err.Error())
	}
	return err
}
