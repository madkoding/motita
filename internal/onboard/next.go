package onboard

import "context"

// chooseOneTwoThree is the reminder shown when a three-option menu gets another answer.
const chooseOneTwoThree = "    Choose 1, 2 or 3."

// chooseNext asks what to do now that the setup is saved: the terminal interface, the gateway
// with its browser interface, or nothing. It is not numbered among the steps - the setup is
// already written - and any answer it cannot read (end of input, Ctrl+C, three invalid tries)
// means NextExit, never an error: the user's configuration is safe either way.
func (s *session) chooseNext(ctx context.Context) Next {
	printSection(s.out, "What next?")
	s.say("")
	m := s.showMenu(
		[]string{"Continue in the TUI", "Start the gateway (WebUI)", "Exit"},
		[]string{"open the terminal interface now", "serve the browser interface from this machine",
			"come back any time with: motita"})
	for attempt := 0; attempt < 3; attempt++ {
		answer, err := s.choose(ctx, "Next [1]:", m)
		if err != nil {
			return NextExit
		}
		switch answer {
		case "", "1":
			return NextTUI
		case "2":
			return NextGateway
		case "3":
			return NextExit
		default:
			s.say(chooseOneTwoThree)
		}
	}
	return NextExit
}
