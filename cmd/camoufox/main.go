// Command camoufox is a small CLI for the pure-Go Camoufox SDK: download the
// browser, print versions, and run a quick navigation + screenshot.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	camoufox "github.com/yx-zero/camoufox-go"
	"github.com/yx-zero/camoufox-go/fetch"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "fetch":
		cmdFetch(os.Args[2:])
	case "version":
		cmdVersion(os.Args[2:])
	case "run":
		cmdRun(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `camoufox - pure-Go Camoufox SDK CLI

Usage:
  camoufox fetch [-cache DIR]                 Download & cache the Camoufox browser
  camoufox version [-cache DIR]               Show installed browser + SDK info
  camoufox run -url URL [flags]               Launch, navigate, report, screenshot

run flags:
  -url URL          Page to open (required)
  -screenshot FILE  Save a PNG screenshot to FILE
  -headful          Run with a visible window (default headless)
  -os OS            Fingerprint OS: windows|macos|linux (default random)
  -timeout DUR      Overall timeout (default 120s)
`)
}

func cmdFetch(args []string) {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	cache := fs.String("cache", "", "cache directory")
	fs.Parse(args)

	ctx, stop := signalCtx()
	defer stop()

	var lastPct int
	path, err := fetch.Fetch(ctx, fetch.Options{
		CacheDir: *cache,
		Progress: func(done, total int64) {
			if total > 0 {
				pct := int(done * 100 / total)
				if pct != lastPct {
					lastPct = pct
					fmt.Printf("\rdownloading: %d%%", pct)
				}
			}
		},
	})
	fmt.Println()
	if err != nil {
		fatal(err)
	}
	fmt.Println("installed:", path)
}

func cmdVersion(args []string) {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	cache := fs.String("cache", "", "cache directory")
	fs.Parse(args)

	v, err := fetch.InstalledVersion(fetch.Options{CacheDir: *cache})
	if err != nil {
		fmt.Println("browser: not installed (run `camoufox fetch`)")
	} else {
		fmt.Printf("browser: %s\n", v.FullString())
	}
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	url := fs.String("url", "", "URL to open (required)")
	shot := fs.String("screenshot", "", "save screenshot PNG to this path")
	headful := fs.Bool("headful", false, "run with a visible window")
	osName := fs.String("os", "", "fingerprint OS: windows|macos|linux")
	timeout := fs.Duration("timeout", 120*time.Second, "overall timeout")
	fs.Parse(args)

	if *url == "" {
		fmt.Fprintln(os.Stderr, "run: -url is required")
		os.Exit(2)
	}

	ctx, stop := signalCtx()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	fmt.Println("launching camoufox...")
	br, err := camoufox.Launch(ctx, camoufox.Options{
		Headless: !*headful,
		OS:       *osName,
		Progress: func(done, total int64) {
			if total > 0 {
				fmt.Printf("\rdownloading browser: %d%%", int(done*100/total))
			}
		},
	})
	if err != nil {
		fatal(err)
	}
	defer br.Close()

	pg, err := br.NewPage(ctx)
	if err != nil {
		fatal(err)
	}

	fmt.Println("navigating:", *url)
	if err := pg.Goto(ctx, *url); err != nil {
		fatal(err)
	}

	ua, _ := pg.EvaluateString(ctx, "navigator.userAgent")
	wd, _ := pg.Evaluate(ctx, "navigator.webdriver")
	plat, _ := pg.EvaluateString(ctx, "navigator.platform")
	title, _ := pg.Title(ctx)

	fmt.Println("title:           ", title)
	fmt.Println("navigator.userAgent:", ua)
	fmt.Println("navigator.platform: ", plat)
	fmt.Printf("navigator.webdriver: %s\n", string(wd))

	if *shot != "" {
		png, err := pg.Screenshot(ctx)
		if err != nil {
			fatal(err)
		}
		if err := os.WriteFile(*shot, png, 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("screenshot saved: %s (%d bytes)\n", *shot, len(png))
	}
}

func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
