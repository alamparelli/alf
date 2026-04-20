package marketplace

import (
	"context"

	"github.com/alamparelli/alf/internal/capability"
)

// appCapability adapts an installed AppInfo to capability.Capability.
// Kind=KindApp. Execute surfaces the AppInfo as Output.Data — the Runtime
// (Step 4) decides whether to open the iframe, invoke a declared tool,
// or list the app's ToolDecls.
//
// Lives in marketplace/ to preserve the capability ← marketplace edge.
type appCapability struct {
	app AppInfo
}

func asCapability(app AppInfo) capability.Capability {
	return appCapability{app: app}
}

func (a appCapability) Manifest() capability.Manifest {
	return capability.Manifest{
		ID:          capability.ID(a.app.Slug),
		Kind:        capability.KindApp,
		Name:        a.app.Name,
		Version:     a.app.Version,
		Description: a.app.Description,
		Permissions: a.permissions(),
	}
}

func (a appCapability) Permissions() capability.PermissionSet {
	return a.permissions()
}

// permissions projects the app's declared secret services onto the
// Capability's PermissionSet. Filesystem / network rules come from the
// Sandbox block (Step 3) — apps don't carry explicit paths today.
func (a appCapability) permissions() capability.PermissionSet {
	var secrets []string
	if len(a.app.Services) > 0 {
		secrets = append(secrets, a.app.Services...)
	}
	return capability.PermissionSet{Secrets: secrets}
}

// Execute returns the AppInfo as-is. Apps are long-running iframe + backend
// services; they are not invoked one-shot through Capability.Execute. The
// Runtime inspects Output.Data to route (open iframe, list tools, etc.).
func (a appCapability) Execute(_ context.Context, _ capability.Input) (capability.Output, error) {
	return capability.Output{Data: a.app}, nil
}

// MirrorInto registers every app currently known to mgr into reg as a
// KindApp Capability. Idempotent via Replace — safe to call on every
// install / uninstall / update via Manager.SetOnChange.
func MirrorInto(mgr *Manager, reg *capability.Registry) error {
	if mgr == nil || reg == nil {
		return nil
	}
	for _, app := range mgr.List() {
		if app.Slug == "" {
			continue
		}
		if err := reg.Replace(asCapability(app)); err != nil {
			return err
		}
	}
	return nil
}
