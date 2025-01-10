package kcobra

import (
	"fmt"
	"github.com/System233/enkit/lib/kflags"
	"github.com/System233/enkit/lib/multierror"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type PFlag struct {
	*pflag.Flag
}

func (pf *PFlag) Name() string {
	return pf.Flag.Name
}

func (pf *PFlag) Set(value string) error {
	err := pf.Flag.Value.Set(value)
	if err != nil {
		return err
	}
	pf.Flag.DefValue = value
	return nil
}

func (pf *PFlag) SetContent(origin string, data []byte) error {
	def, err := kflags.SetContent(pf.Flag.Value, origin, data)
	if err != nil {
		return err
	}
	pf.Flag.DefValue = def
	return nil
}

type KCommand struct {
	*cobra.Command
}

func (kc *KCommand) Hide(hidden bool) {
	kc.Command.Hidden = hidden
}

func (kc *KCommand) AddCommand(def kflags.CommandDefinition, flags []kflags.FlagDefinition, action kflags.CommandAction) error {
	command := &cobra.Command{
		// In cobra, the name of a command is the first word in the use string.
		Use:     def.Name + " " + def.Use,
		Short:   def.Short,
		Long:    def.Long,
		Example: def.Example,
		Aliases: def.Aliases,
	}

	flargs := []kflags.FlagArg{}
	set := command.PersistentFlags()
	for ix, flag := range flags {
		set.String(flag.Name, flag.Default, flag.Help)
		fdef := set.Lookup(flag.Name)
		if fdef == nil {
			return fmt.Errorf("internal error: the flag %s was just created, and yet does not exist - nil was returned", flag.Name)
		}

		flargs = append(flargs, kflags.FlagArg{
			FlagDefinition: &flags[ix],
			Value:          fdef.Value,
		})
	}

	if action != nil {
		command.RunE = func(cmd *cobra.Command, args []string) error {
			return action(flargs, args)
		}
	} else {
		command.RunE = func(cmd *cobra.Command, args []string) error {
			return pflag.ErrHelp
		}
	}

	kc.Command.AddCommand(command)
	return nil
}

// CobraPopulator returns a kflags.Populator capable of filling in the defaults for
// flags defined through cobra and the pflags library.
func CobraPopulator(root *cobra.Command, args []string) kflags.Populator {
	return func(resolvers ...kflags.Augmenter) error {
		return PopulateDefaults(root, args, resolvers...)
	}
}

func PopulateDefaults(root *cobra.Command, args []string, resolvers ...kflags.Augmenter) error {
	if err := PopulateCommands(root, args, resolvers...); err != nil {
		return err
	}

	if err := PopulateFlagDefaults(root, args, resolvers...); err != nil {
		return err
	}

	var errs []error
	for _, r := range resolvers {
		if err := r.Done(); err != nil {
			errs = append(errs, err)
		}
	}

	return multierror.New(errs)
}

func PopulateCommands(root *cobra.Command, args []string, resolvers ...kflags.Augmenter) error {
	if len(args) >= 1 {
		args = args[1:]
	}

	// Find the actual cobra command that would be run given the current argv.
	var errs []error
	var previous *cobra.Command
	for {
		target, _, _ := root.Find(args)
		if target == previous {
			break
		}
		previous = target

		namespace := target.Name()
		for cursor := target.Parent(); cursor != nil; cursor = cursor.Parent() {
			namespace = cursor.Name() + "." + namespace
		}

		ktarget := &KCommand{target}
		for _, r := range resolvers {
			if _, err := r.VisitCommand(namespace, ktarget); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return multierror.New(errs)
}

// PopulateFlagDefaults is a function that walks all the flags of the specified root command
// and all its sub commands, the argv provided as args, and tries to provide defaults
// using the spcified resolvers.
//
// root is the cobra.Command of which to walk the flags to fill in the defaults.
//
// args is the list of command line parameters passed to the command, argv. This is
// generally os.Args. It is expected to include argv[0], the path of the command, as
// first argument.
//
// resolvers is the list of resolvers to use to assign the defaults.
//
// WARNING: PopulateFlagDefaults does not call Done() supplied list of resolvers.
// It is the responsibility of the caller to do so.
func PopulateFlagDefaults(root *cobra.Command, args []string, resolvers ...kflags.Augmenter) error {
	// argv[0] needs to be skipped, args is generally os.Args, which contains argv 0.
	if len(args) >= 1 {
		args = args[1:]
	}

	// Find the actual cobra command that would be run given the current argv.
	target, _, _ := root.Find(args)

	// Augmenters need to assign defaults from more generic config to more specific configs.
	// Parent command is considered more generic than child command.
	// Walk back the root, so we can override defaults in the correct order.
	stack := []*cobra.Command{}
	for cmd := target; cmd != nil; cmd = cmd.Parent() {
		stack = append(stack, cmd)
	}

	// According to cobra documentation, command.Flags() is supposed to return all the
	// flags of a command.
	//
	// By looking at the source code, this seems to be the case only if the internal method
	// mergePersistentFlags() was called before Flags() is invoked.
	// Given that mergePersistentFlags() is an internal method, there is no future-proof way
	// to determine if it has been called or not when PopulateDefaults is used.
	//
	// For example, at the time of writing (04/2020), the method Find() invoked above will
	// cause  mergePersistentFlags to be invoked only if the supplied args is non empty. But
	// in this function, we need to iterate on all flags regardless of what the args were.
	//
	// For defense in depth, the code below ignores the whole ordeal. Simply iterates over all
	// LocalFlags() and InheritedFlags() of all commands involved - merge or not should be irrelevant.
	// As a side benefit, this makes the code here tolerant to the TraverseChildren option
	// being used on cobra.Command.
	//
	// However, this may (or may not) cause the same flags to be processed multiple times.
	// We protect against this by using the seen map.
	errs := []error{}
	resolve := func(namespace string, seen map[string]struct{}, r kflags.Augmenter, flag *pflag.Flag) {
		// Prevent setting the same flag multiple times, defense in depth - see comment above.
		_, found := seen[flag.Name]
		if found {
			return
		}
		// Prevent setting flags that the user set manually.
		if flag.Changed {
			return
		}

		seen[flag.Name] = struct{}{}
		if _, err := r.VisitFlag(namespace, &PFlag{flag}); err != nil {
			errs = append(errs, err)
			return
		}
	}

	name := ""
	for ix := range stack {
		cmd := stack[len(stack)-ix-1]

		if name != "" {
			name += "."
		}
		name = name + cmd.Name()

		seen := map[string]struct{}{}

		for _, r := range resolvers {
			resolver := func(flag *pflag.Flag) {
				resolve(name, seen, r, flag)
			}

			cmd.InheritedFlags().VisitAll(resolver)
			cmd.LocalFlags().VisitAll(resolver)
		}
	}

	return multierror.New(errs)
}
