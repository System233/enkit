package main

import (
	"github.com/System233/enkit/lib/client"
	"github.com/System233/enkit/lib/kflags/kcobra"
	"github.com/System233/enkit/machinist/mserver"
)

func main() {
	base := client.DefaultBaseFlags("astore", "enkit")

	root := mserver.NewCommand(base)
	set, populator, runner := kcobra.Runner(root, nil, base.IdentityErrorHandler("astore login"))

	base.Run(set, populator, runner)
}
