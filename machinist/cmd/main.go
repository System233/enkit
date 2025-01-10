package main

import (
	"github.com/System233/enkit/lib/client"
	"github.com/System233/enkit/lib/kflags/kcobra"
	"github.com/System233/enkit/machinist"
)

func main() {
	base := client.DefaultBaseFlags("astore", "enkit")
	c := machinist.NewRootCommand(base)

	set, populator, runner := kcobra.Runner(c, nil, base.IdentityErrorHandler("enkit login"))

	base.Run(set, populator, runner)
}
