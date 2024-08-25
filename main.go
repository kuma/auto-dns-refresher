package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/dns/v1"
	"google.golang.org/api/option"
)

func getExternalIP() (string, error) {
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(ip), nil
}

func getARecordFromCloudDNS(projectID, managedZone, domain string) ([]string, error) {
	ctx := context.Background()
	dnsService, err := dns.NewService(ctx, option.WithCredentialsFile("key.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS service: %v", err)
	}

	rrsets, err := dnsService.ResourceRecordSets.List(projectID, managedZone).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list resource record sets: %v", err)
	}

	var aRecords []string
	for _, rrset := range rrsets.Rrsets {
		if rrset.Type == "A" && rrset.Name == domain+"." {
			aRecords = append(aRecords, rrset.Rrdatas...)
		}
	}

	return aRecords, nil
}

func setARecord(projectID, managedZone, domain, ip string) error {
	ctx := context.Background()
	dnsService, err := dns.NewService(ctx, option.WithCredentialsFile("key.json"))
	if err != nil {
		return fmt.Errorf("failed to create DNS service: %v", err)
	}

	// Check if the A record already exists
	existingRecords, err := getARecordFromCloudDNS(projectID, managedZone, domain)
	if err != nil {
		return fmt.Errorf("failed to get existing A records: %v", err)
	}

	rrset := &dns.ResourceRecordSet{
		Name:    domain + ".",
		Type:    "A",
		Ttl:     300,
		Rrdatas: []string{ip},
	}

	change := &dns.Change{}

	if len(existingRecords) > 0 {
		// If the record exists, create a change to delete the old record and add the new one
		oldRrset := &dns.ResourceRecordSet{
			Name:    domain + ".",
			Type:    "A",
			Ttl:     300,
			Rrdatas: existingRecords,
		}
		change.Deletions = []*dns.ResourceRecordSet{oldRrset}
	}

	change.Additions = []*dns.ResourceRecordSet{rrset}

	_, err = dnsService.Changes.Create(projectID, managedZone, change).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to create DNS change: %v", err)
	}

	return nil
}

func postToGoogleChat(webhookURL, message string) error {
	payload := map[string]string{"text": message}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %v", err)
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to send POST request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non-OK response: %v", resp.Status)
	}

	return nil
}

func main() {
	domains := strings.Split(os.Getenv("DOMAINS"), ",")
	projectID := os.Getenv("PROJECT_ID")
	managedZone := os.Getenv("MANAGED_ZONE")
	webhookURL := os.Getenv("WEBHOOK_URL")
	waitInMinuteStr := os.Getenv("WAIT_IN_MINUTE")
	waitInMinute, err := strconv.Atoi(waitInMinuteStr)
	if err != nil {
		fmt.Println("Error converting WAIT_IN_MINUTE to int:", err)
		return
	}

	for {
		ip, err := getExternalIP()
		if err != nil {
			fmt.Println("Error:", err)
			postToGoogleChat(webhookURL, err.Error())
			return
		}

		for _, domain := range domains {
			aRecords, err := getARecordFromCloudDNS(projectID, managedZone, domain)
			if err != nil {
				fmt.Println("Error:", err)
				postToGoogleChat(webhookURL, err.Error())
			}

			if aRecords[0] == ip {
				fmt.Println("A records for", domain, "already set to", ip)
				continue
			}
			fmt.Println("Setting A records for", domain, "to", ip)
			postToGoogleChat(webhookURL, "Setting A records for "+domain+" to "+ip)
			err = setARecord(projectID, managedZone, domain, ip)
			if err != nil {
				fmt.Println("Error:", err)
				postToGoogleChat(webhookURL, err.Error())
			}
		}
		time.Sleep(time.Duration(waitInMinute) * time.Minute)
	}
}
