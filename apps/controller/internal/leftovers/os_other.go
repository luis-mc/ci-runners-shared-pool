//go:build !windows && !linux && !darwin

package leftovers

import (
	"context"
	"os"
)

type hostOS struct{ hostFS }

// NewHostOS on a platform without runner services finds nothing.
func NewHostOS() OS { return &hostOS{hostFS{root: "/var/lib/crsp/runners"}} }

func (*hostOS) GOOS() string                                  { return "other" }
func (*hostOS) Admin() bool                                   { return os.Geteuid() == 0 }
func (*hostOS) Services(context.Context) ([]OSService, error) { return nil, nil }
func (*hostOS) Accounts(context.Context) ([]OSAccount, error) { return nil, nil }
func (*hostOS) StopService(context.Context, string) error     { return ErrNotExist }
func (*hostOS) DeleteService(context.Context, string) error   { return ErrNotExist }
func (*hostOS) DeleteAccount(context.Context, string) error   { return ErrNotExist }
