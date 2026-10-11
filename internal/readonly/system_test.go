package readonly

import "testing"

// The follow-up to A4: readers of system state whose arguments change it. Each refused form ran
// in plan mode before; each reading form beside it must still run.

func TestSystemReadersRefuseTheFormsThatChangeTheSystem(t *testing.T) {
	for _, c := range []argv{
		{"journalctl", "--vacuum-size=1M"}, {"journalctl", "--vacuum-time", "1s"}, {"journalctl", "--vacuum-files=1"},
		{"journalctl", "--rotate"}, {"journalctl", "--flush"}, {"journalctl", "--sync"},
		{"journalctl", "--relinquish-var"}, {"journalctl", "--smart-relinquish-var"},
		{"journalctl", "--setup-keys"}, {"journalctl", "--update-catalog"},
		{"dmesg", "-c"}, {"dmesg", "-C"}, {"dmesg", "-D"}, {"dmesg", "-E"}, {"dmesg", "-n", "1"},
		{"dmesg", "-Tc"}, {"dmesg", "--clear"}, {"dmesg", "--read-clear"}, {"dmesg", "--console-off"},
		{"dmesg", "--console-on"}, {"dmesg", "--console-level=1"},
		{"date", "-s", "2020-01-01"}, {"date", "--set=2020-01-01"}, {"date", "--set", "x"},
		{"date", "-us", "x"}, {"date", "010100002020"}, {"date", "-u", "0101"},
		{"date", "-d", "now", "0101"}, {"date", "--date=now", "0101"},
		{"hostname", "evil"}, {"hostname", "-F", "/tmp/x"}, {"hostname", "--file=/tmp/x"},
		{"hostname", "-b"}, {"hostname", "--boot"},
		{"hostnamectl", "set-hostname", "x"}, {"hostnamectl", "hostname", "x"},
		{"hostnamectl", "--static", "set-hostname", "x"}, {"hostnamectl", "icon-name", "x"},
		{"timedatectl", "set-time", "12:00"}, {"timedatectl", "set-timezone", "UTC"},
		{"timedatectl", "set-ntp", "false"}, {"timedatectl", "set-local-rtc", "1"},
		{"timedatectl", "ntp-servers", "eth0", "x"}, {"timedatectl", "revert", "eth0"},
		{"ip", "link", "set", "eth0", "down"}, {"ip", "addr", "add", "1.2.3.4/24", "dev", "eth0"},
		{"ip", "a", "del", "1.2.3.4/24", "dev", "eth0"}, {"ip", "route", "flush", "all"},
		{"ip", "route", "replace", "default", "via", "x"}, {"ip", "netns", "exec", "x", "id"},
		{"ip", "-b", "cmds"}, {"ip", "-batch", "cmds"}, {"ip", "--batch", "cmds"}, {"ip", "-force", "-b", "x"},
		{"ip", "-n", "x", "link", "set", "lo", "up"}, {"ip", "neigh", "flush", "all"},
		{"route", "add", "default", "gw", "x"}, {"route", "del", "default"}, {"route", "-A", "inet6", "add", "x"},
		{"ifconfig", "eth0", "down"}, {"ifconfig", "eth0", "1.2.3.4"}, {"ifconfig", "-a", "eth0", "mtu", "1"},
		{"arp", "-d", "1.2.3.4"}, {"arp", "-s", "1.2.3.4", "x"}, {"arp", "-f", "file"},
		{"arp", "--delete", "x"}, {"arp", "--set", "x"}, {"arp", "--file=x"}, {"arp", "-nd", "x"},
		{"ag", "--pager", "./x", "p"}, {"ag", "--pager=./x", "p"},
		{"ldd", "./bin"},
	} {
		if d := Check(c[0], c[1:]); d.Allowed {
			t.Errorf("%q changes the system and must be refused, got allowed: %s", c, d.Reason)
		} else if d.Reason == "" {
			t.Errorf("%q: a refusal must explain itself", c)
		}
	}
}

func TestSystemReadersStillRead(t *testing.T) {
	for _, c := range []argv{
		{"journalctl"}, {"journalctl", "-u", "nginx", "-n", "50"}, {"journalctl", "--since=today"},
		{"journalctl", "--disk-usage"},
		{"dmesg"}, {"dmesg", "-T"}, {"dmesg", "--level=err"}, {"dmesg", "-l", "err"}, {"dmesg", "--human"},
		{"date"}, {"date", "+%s"}, {"date", "-u", "+%F"}, {"date", "-d", "yesterday"}, {"date", "-dnow"},
		{"date", "--date", "now", "+%s"}, {"date", "--date=now"}, {"date", "-r", "file"}, {"date", "-Iseconds"}, {"date", "-I", "+%s"},
		{"date", "--rfc-3339=s"}, {"date", "-f", "dates"},
		{"hostname"}, {"hostname", "-f"}, {"hostname", "-I"}, {"hostname", "--fqdn"},
		{"hostnamectl"}, {"hostnamectl", "status"}, {"hostnamectl", "hostname"}, {"hostnamectl", "--static", "hostname"},
		{"timedatectl"}, {"timedatectl", "status"}, {"timedatectl", "show"}, {"timedatectl", "list-timezones"},
		{"timedatectl", "timesync-status"}, {"timedatectl", "show-timesync"},
		{"ip", "a"}, {"ip", "addr"}, {"ip", "-br", "a"}, {"ip", "-brief", "link", "show"}, {"ip", "a", "s"},
		{"ip", "route", "show"}, {"ip", "route", "get", "1.1.1.1"}, {"ip", "-4", "route", "list"},
		{"ip", "-n", "x", "link"}, {"ip", "-family", "inet", "addr", "show"}, {"ip", "neigh", "ls"},
		{"ip", "link", "help"}, {"ip", "-j", "-p", "link", "lst"},
		{"route"}, {"route", "-n"}, {"route", "-A", "inet6"},
		{"ifconfig"}, {"ifconfig", "-a"}, {"ifconfig", "eth0"},
		{"arp"}, {"arp", "-n"}, {"arp", "-a"}, {"arp", "-i", "eth0"},
		{"ag", "pattern"}, {"ag", "--nopager", "p"},
	} {
		if d := Check(c[0], c[1:]); !d.Allowed {
			t.Errorf("%q only reads and must be allowed, got: %s", c, d.Reason)
		}
	}
}
