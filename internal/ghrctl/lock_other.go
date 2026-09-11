//go:build !linux

package ghrctl

import "errors"

func lock(string) (func(), error)      { return nil, errors.New("ghrctl deployments require Linux x64") }
func socketGID(string) (uint32, error) { return 0, errors.New("ghrctl deployments require Linux x64") }
