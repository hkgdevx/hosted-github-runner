package ghrctl

import (
	"context"
	"errors"
	"fmt"
)

func (a app) recreate(ctx context.Context, cmd string, c Config, token string, con *container, o options) error {
	if e := a.d.legacy(ctx); e != nil {
		return e
	}
	if e := a.busy(ctx, c, token, con, o.force); e != nil {
		return e
	}
	target := c.Image
	if cmd == "upgrade" {
		target = a.build.Image
		if o.rollback {
			target = c.PreviousImage
		}
		if target == "" {
			return errors.New("no target image available for this upgrade/rollback")
		}
		if target == c.Image {
			if con == nil || !con.State.Running {
				return errors.New("target is already selected but runner is not running; use ghrctl start or restart")
			}
			fmt.Fprintln(a.out, "Selected image already matches target; checking readiness.")
			return a.ready(ctx, c, token, o.timeout)
		}
	}
	if !imageRef.MatchString(target) {
		return errors.New("invalid target image")
	}
	proposed := c
	proposed.Image = target
	if e := a.d.validateCompose(ctx, proposed, token); e != nil {
		return e
	}
	// All downloads and permission checks happen before disrupting the runner.
	if e := a.d.run(ctx, "pull", target); e != nil {
		return e
	}
	if e := a.stop(ctx, c, token, con, o.force); e != nil {
		return e
	}
	if cmd == "upgrade" {
		c.PreviousImage, c.Image = c.Image, target
		if e := save(a.dir, c, token); e != nil {
			return fmt.Errorf("runner stopped but configuration could not be saved: %w; inspect configuration before restarting", e)
		}
	}
	// The target was already pulled; avoid a second network dependency after shutdown.
	if e := a.deploy(ctx, c, token); e != nil {
		return a.upgradeError(cmd, e)
	}
	return a.upgradeError(cmd, a.ready(ctx, c, token, o.timeout))
}

func (a app) upgradeError(cmd string, e error) error {
	if e != nil && cmd == "upgrade" {
		return fmt.Errorf("%w; inspect status/logs; run ghrctl upgrade --rollback to restore the previous image", e)
	}
	return e
}

func (a app) deploy(ctx context.Context, c Config, token string) error {
	if e := a.gh.validate(ctx, c, token, 0); e != nil {
		return e
	}
	gid, e := socketGID(a.d.socketPath())
	if e != nil {
		return e
	}
	if e = a.d.apply(c, token, gid); e != nil {
		return e
	}
	if e = a.d.compose(ctx, "config", "--quiet"); e != nil {
		return e
	}
	return a.d.compose(ctx, "up", "-d")
}
