package command

import "time"

type Extra struct {
	On, God, Speeds bool
	Lift, Down      time.Time
	Timing          Frames
}

func init() {
	off := func(c *Console) bool { return !c.X.On }
	add := func(name string, run func(env Env, x *Extra, args []string, out *Out)) {
		Register(Cmd{Name: name, Unlisted: true, Off: off, Order: 1000, Run: func(env Env, args []string, out *Out) error {
			run(env, &env.Console().X, args, out)
			return nil
		}})
	}
	flag := func(name string, v bool, out *Out) {
		n := "0"
		if v {
			n = "1"
		}
		out.Line(T(`"` + name + `" is "` + n + `"`))
	}
	onOff := func(s string, v bool) string {
		if v {
			return s + " ON"
		}
		return s + " OFF"
	}
	Register(Cmd{Name: "sv_cheats", Unlisted: true, Order: 1000, Run: func(env Env, args []string, out *Out) error {
		x := &env.Console().X
		switch {
		case len(args) == 0:
			flag("sv_cheats", x.On, out)
		case args[0] == "1":
			x.On = true
			env.Say("You have found a secret area")
		case args[0] == "0":
			*x = Extra{}
		}
		return nil
	}})
	add("god", func(_ Env, x *Extra, _ []string, out *Out) {
		x.God = !x.God
		out.Line(T(onOff("godmode", x.God)))
	})
	add("noclip", func(_ Env, x *Extra, _ []string, out *Out) {
		x.Lift = time.Now()
		out.Line(T(onOff("noclip", true)))
	})
	add("kill", func(_ Env, x *Extra, _ []string, _ *Out) { x.Down = time.Now() })
	add("impulse", func(env Env, _ *Extra, args []string, out *Out) {
		if len(args) == 0 || args[0] != "9" {
			return
		}
		out.Line(T("You got the gear"))
		for _, g := range env.Gear() {
			out.Line(S(g, Value))
		}
	})
	add("r_speeds", func(_ Env, x *Extra, args []string, out *Out) {
		switch {
		case len(args) == 0:
			flag("r_speeds", x.Speeds, out)
		case args[0] == "1":
			x.Speeds = true
		case args[0] == "0":
			x.Speeds = false
		}
	})
	add("map", func(_ Env, _ *Extra, args []string, out *Out) {
		if len(args) == 0 {
			out.Line(T("map <name>"))
			return
		}
		out.Line(T(args[0] + " isn't on this drive."))
	})
}
