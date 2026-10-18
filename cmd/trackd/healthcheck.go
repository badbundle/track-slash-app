package main

import (
	"fmt"
	"net/http"
	"time"
)

const healthcheckTimeout = 3 * time.Second

// checkHealth asks the trackd server at baseURL whether it is serving and
// can reach Postgres. The image is distroless, with no shell or curl, so its
// HEALTHCHECK runs trackd -healthcheck, which calls this.
func checkHealth(baseURL string) error {
	client := &http.Client{Timeout: healthcheckTimeout}
	res, err := client.Get(baseURL + "/api/v1/healthz")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %s", res.Status)
	}
	return nil
}
