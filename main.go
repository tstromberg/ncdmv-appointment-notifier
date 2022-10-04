// ncdmv-appointment-finder notifies you when there is an available appointment
package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/gen2brain/beeep"
	"k8s.io/klog/v2"
)

var (
	refreshFlag    = flag.Duration("refresh-delay", 9*time.Minute, "how long to wait between refreshes if no matching appointments are found")
	foundDelayFlag = flag.Duration("found-delay", 60*time.Minute, "how long to wait between refreshes if a matching appointment is found")
	sitesDirFlag   = flag.String("sites-directory", "sites/", "path to sites content")
	maxNotifyDays  = flag.Int("max-notify-days", 30, "only notify if a reservation is available within this many days")
	runCommand     = flag.String("run-command", "say", "alert command to run (must accept a string argument)")

	dateForm = "2006-01-02"

	nextDateRe = regexp.MustCompile(`var Dates = \[ "([\d-]+)".* \];`)
	locRe      = regexp.MustCompile(`<div id=".*" class="displaydata-text"><div>(.*?)</div></div>`)
)

func nextAppt(sitePath string) (time.Time, string, error) {
	klog.Infof("checking %s ...", sitePath)
	out, err := exec.Command("/bin/sh", sitePath).Output()
	if err != nil {
		return time.Time{}, "", fmt.Errorf("site command failed: %v", err)
	}
	klog.Infof("%q returned %d bytes of data", sitePath, len(out))

	// Depending on the browser used to generate the cURL command, the content may be compressed
	r := bytes.NewReader(out)
	gzr, err := gzip.NewReader(r)
	if err == nil {
		outz, err := ioutil.ReadAll(gzr)
		if err == nil {
			out = outz
		}
	}

	next := time.Time{}
	// klog.Infof("data: %s", string(out))
	for _, m := range nextDateRe.FindAllStringSubmatch(string(out), -1) {
		// klog.Infof("date match: %v", m)
		t, err := time.Parse(dateForm, m[1])
		if err != nil {
			return time.Time{}, "", fmt.Errorf("unable to parse time %q: %w", t, err)
		}
		next = t
	}

	loc := ""
	for _, m := range locRe.FindAllStringSubmatch(string(out), -1) {
		loc = m[1]
	}

	if loc == "" {
		return next, loc, fmt.Errorf("failed to parse location for %q: %s", sitePath, string(out))
	}
	return next, loc, nil
}

func notify(loc string, t time.Time, cmd string) error {
	klog.Infof("%s has an appointment available at %s!", loc, t)
	daysUntil := int(time.Until(t).Hours() / 24)
	text := fmt.Sprintf("Next appointment for %s is in %d days", loc, daysUntil)

	klog.Infof("Sending alert: %s", text)
	err := beeep.Alert(text, t.Format(dateForm), "assets/information.png")
	if err != nil {
		return err
	}

	c, err := exec.LookPath(cmd)
	if err != nil {
		klog.Errorf("unable to find %s: %v", cmd, err)
		return nil
	}

	klog.Infof("Running %s ...", cmd)
	return exec.Command(c, text).Run()
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	for {
		found := 0
		fi, err := ioutil.ReadDir(*sitesDirFlag)
		if err != nil {
			klog.Fatalf("unable to list sites-directory %q: %v", *sitesDirFlag, err)
		}

		for _, f := range fi {
			date, location, err := nextAppt(filepath.Join(*sitesDirFlag, f.Name()))
			if err != nil {
				klog.Errorf("%s failed: %s", f.Name(), err)
				continue
			}
			if date.IsZero() {
				klog.Infof("%q has no appointments available", location)
				continue
			}

			daysUntil := int(time.Until(date).Hours() / 24)
			klog.Infof("next appointment for %q is %s (%d days)", location, date, daysUntil)

			if daysUntil < *maxNotifyDays {
				found++
				if err := notify(location, date, *runCommand); err != nil {
					klog.Errorf("notify(%q, %s) failed: %v", location, date, err)
				}
			}
		}

		delay := *refreshFlag
		if found > 0 {
			delay = *foundDelayFlag
		}
		klog.Infof("%d matching appointments found. Sleeping for %s ...", found, foundDelayFlag)
		time.Sleep(delay)
	}
}
