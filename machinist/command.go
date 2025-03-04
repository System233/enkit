package machinist

import (
	"github.com/System233/enkit/lib/client"
	"github.com/System233/enkit/machinist/config"
	"github.com/System233/enkit/machinist/machine"
	"github.com/System233/enkit/machinist/mserver"
	"github.com/spf13/cobra"
)

func NewRootCommand(bf *client.BaseFlags) *cobra.Command {
	c := &cobra.Command{
		Use: "machinist",
	}
	conf := &config.Common{
		Root: bf,
	}
	c.PersistentFlags().StringVar(&conf.ControlPlaneHost, "control-host", "localhost", "")
	c.PersistentFlags().IntVar(&conf.ControlPlanePort, "control-port", 4545, "")
	c.PersistentFlags().IntVar(&conf.MetricsPort, "metrics-port", 9090, "")
	c.PersistentFlags().BoolVar(&conf.EnableMetrics, "metrics-enable", true, "")
	c.AddCommand(machine.NewNodeCommand(conf))
	c.AddCommand(mserver.NewCommand(conf.Root))
	return c
}
