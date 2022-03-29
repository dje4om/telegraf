//go:generate go run ../../../scripts/generate_plugindata/main.go
//go:generate go run ../../../scripts/generate_plugindata/main.go --clean
package raindrops

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs"
)

type Raindrops struct {
	Urls       []string
	httpClient *http.Client
}

func (r *Raindrops) SampleConfig() string {
	return `{{ .SampleConfig }}`
}

func (r *Raindrops) Gather(acc telegraf.Accumulator) error {
	var wg sync.WaitGroup

	for _, u := range r.Urls {
		addr, err := url.Parse(u)
		if err != nil {
			acc.AddError(fmt.Errorf("unable to parse address '%s': %s", u, err))
			continue
		}

		wg.Add(1)
		go func(addr *url.URL) {
			defer wg.Done()
			acc.AddError(r.gatherURL(addr, acc))
		}(addr)
	}

	wg.Wait()

	return nil
}

func (r *Raindrops) gatherURL(addr *url.URL, acc telegraf.Accumulator) error {
	resp, err := r.httpClient.Get(addr.String())
	if err != nil {
		return fmt.Errorf("error making HTTP request to %s: %s", addr.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP status %s", addr.String(), resp.Status)
	}
	buf := bufio.NewReader(resp.Body)

	// Calling
	_, err = buf.ReadString(':')
	if err != nil {
		return err
	}
	line, err := buf.ReadString('\n')
	if err != nil {
		return err
	}
	calling, err := strconv.ParseUint(strings.TrimSpace(line), 10, 64)
	if err != nil {
		return err
	}

	// Writing
	_, err = buf.ReadString(':')
	if err != nil {
		return err
	}
	line, err = buf.ReadString('\n')
	if err != nil {
		return err
	}
	writing, err := strconv.ParseUint(strings.TrimSpace(line), 10, 64)
	if err != nil {
		return err
	}
	tags := r.getTags(addr)
	fields := map[string]interface{}{
		"calling": calling,
		"writing": writing,
	}
	acc.AddFields("raindrops", fields, tags)

	iterate := true
	var queuedLineStr string
	var activeLineStr string
	var activeErr error
	var queuedErr error

	for iterate {
		// Listen
		var tags map[string]string

		lis := map[string]interface{}{
			"active": 0,
			"queued": 0,
		}
		activeLineStr, activeErr = buf.ReadString('\n')
		if activeErr != nil {
			break
		}
		if strings.Compare(activeLineStr, "\n") == 0 {
			break
		}
		queuedLineStr, queuedErr = buf.ReadString('\n')
		if queuedErr != nil {
			iterate = false
		}
		activeLine := strings.Split(activeLineStr, " ")
		listenName := activeLine[0]

		active, err := strconv.ParseUint(strings.TrimSpace(activeLine[2]), 10, 64)
		if err != nil {
			active = 0
		}
		lis["active"] = active

		queuedLine := strings.Split(queuedLineStr, " ")
		queued, err := strconv.ParseUint(strings.TrimSpace(queuedLine[2]), 10, 64)
		if err != nil {
			queued = 0
		}
		lis["queued"] = queued
		if strings.Contains(listenName, ":") {
			listener := strings.Split(listenName, ":")
			tags = map[string]string{
				"ip":   listener[0],
				"port": listener[1],
			}
		} else {
			tags = map[string]string{
				"socket": listenName,
			}
		}
		acc.AddFields("raindrops_listen", lis, tags)
	}
	return nil //nolint:nilerr // nil returned on purpose
}

// Get tag(s) for the raindrops calling/writing plugin
func (r *Raindrops) getTags(addr *url.URL) map[string]string {
	h := addr.Host
	host, port, err := net.SplitHostPort(h)
	if err != nil {
		host = addr.Host
		if addr.Scheme == "http" {
			port = "80"
		} else if addr.Scheme == "https" {
			port = "443"
		} else {
			port = ""
		}
	}
	return map[string]string{"server": host, "port": port}
}

func init() {
	inputs.Add("raindrops", func() telegraf.Input {
		return &Raindrops{httpClient: &http.Client{
			Transport: &http.Transport{
				ResponseHeaderTimeout: 3 * time.Second,
			},
			Timeout: 4 * time.Second,
		}}
	})
}
