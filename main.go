package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/riywo/loginshell"
	flag "github.com/spf13/pflag"
)

var version = "unknown"

func printStreamWithTimestamper(r io.Reader, timestamper *Timestamper) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fmt.Println(timestamper.CurrentTimestampString(), scanner.Text())
	}
}

func runCommandWithTimestamper(args []string, timestamper *Timestamper) error {
	command := exec.Command(args[0], args[1:]...)
	ptmx, err := pty.Start(command)
	if err != nil {
		return err
	}
	defer func() { _ = ptmx.Close() }()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigs {
			switch sig {
			case syscall.SIGWINCH:
				if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
					log.Printf("error resizing pty: %s", err)
				}

			case syscall.SIGINT:
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGINT)

			case syscall.SIGTERM:
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)

			default:
			}
		}
	}()
	sigs <- syscall.SIGWINCH

	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()

	printStreamWithTimestamper(ptmx, timestamper)

	return command.Wait()
}

func main() {
	log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))

	var elapsedMode = flag.BoolP("elapsed", "s", false, "show elapsed timestamps")
	var incrementalMode = flag.BoolP("incremental", "i", false, "show incremental timestamps")
	var format = flag.StringP("format", "f", "", "show timestamps in this format")
	var utc = flag.BoolP("utc", "u", false, "show absolute timestamps in UTC")
	var timezoneName = flag.StringP("timezone", "z", "", "show absolute timestamps in this timezone, e.g. America/New_York")
	var printHelp = flag.BoolP("help", "h", false, "print help and exit")
	var printVersion = flag.BoolP("version", "v", false, "print version and exit")
	flag.CommandLine.SortFlags = false
	flag.SetInterspersed(false)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `
ets -- command output timestamper

ets prefixes each line of a command's output with a timestamp.

Usage:

  %s [-s | -i] [-f format] [-u | -z timezone] command [arg ...]
  %s [options] shell_command
  %s [options]

The three usage strings correspond to three command execution modes:

* If given a single command without whitespace(s), or a command and its
  arguments, execute the command with exec in a pty;

* If given a single command with whitespace(s), the command is treated as
  a shell command and executed as SHELL -c shell_command, where SHELL is
  the current user's login shell, or sh if login shell cannot be determined;

* If given no command, output is read from stdin, and the user is
  responsible for piping in a command's output.

There are three mutually exclusive timestamp modes:

* The default is absolute time mode, where timestamps from the wall clock
  are shown;

* -s, --elapsed turns on elapsed time mode, where every timestamp is the
  time elapsed from the start of the command (using a monotonic clock);

* -i, --incremental turns on incremental time mode, where every timestamp is
  the time elapsed since the last timestamp (using a monotonic clock).

The default format of the prefixed timestamps depends on the timestamp mode
active. Users may supply a custom format string with the -f, --format option.
The format string is basically a strftime(3) format string; see the man page
or README for details on supported formatting directives.

The timezone for absolute timestamps can be controlled via the -u, --utc
and -z, --timezone options. --timezone accepts IANA time zone names, e.g.,
America/Los_Angeles. Local time is used by default.

Options:
`, os.Args[0], os.Args[0], os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if *printHelp {
		flag.Usage()
		os.Exit(0)
	}

	if *printVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	mode := AbsoluteTimeMode
	if *elapsedMode && *incrementalMode {
		log.Fatal("conflicting flags --elapsed and --incremental")
	}
	if *elapsedMode {
		mode = ElapsedTimeMode
	}
	if *incrementalMode {
		mode = IncrementalTimeMode
	}
	if *format == "" {
		if mode == AbsoluteTimeMode {
			*format = "[%F %T]"
		} else {
			*format = "[%T]"
		}
	}
	timezone := time.Local
	if *utc && *timezoneName != "" {
		log.Fatal("conflicting flags --utc and --timezone")
	}
	if *utc {
		timezone = time.UTC
	}
	if *timezoneName != "" {
		location, err := time.LoadLocation(*timezoneName)
		if err != nil {
			log.Fatal(err)
		}
		timezone = location
	}
	args := flag.Args()

	timestamper, err := NewTimestamper(*format, mode, timezone)
	if err != nil {
		log.Fatal(err)
	}

	exitCode := 0
	if len(args) == 0 {
		printStreamWithTimestamper(os.Stdin, timestamper)
	} else {
		if len(args) == 1 {
			arg0 := args[0]
			if matched, _ := regexp.MatchString(`\s`, arg0); matched {
				shell, err := loginshell.Shell()
				if err != nil {
					shell = "sh"
				}
				args = []string{shell, "-c", arg0}
			}
		}
		if err = runCommandWithTimestamper(args, timestamper); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				log.Fatal(err)
			}
		}
	}
	os.Exit(exitCode)
}