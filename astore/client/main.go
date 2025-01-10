package main

import (
	acommands "github.com/System233/enkit/astore/client/commands"
	bcommands "github.com/System233/enkit/lib/client/commands"

	"github.com/System233/enkit/lib/client"
	"github.com/System233/enkit/lib/kflags/kcobra"

	"github.com/System233/enkit/lib/srand"
	"math/rand"
)

func main() {
	base := client.DefaultBaseFlags("astore", "enkit")
	root := acommands.New(base)

	set, populator, runner := kcobra.Runner(root.Command, nil, base.IdentityErrorHandler("astore login"))

	rng := rand.New(srand.Source)
	root.AddCommand(bcommands.NewLogin(base, rng, populator).Command)

	base.Run(set, populator, runner)
}
