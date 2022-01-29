package controllers

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-logr/logr"
)

// CLOUDFLARE_ENDPOINT is the Cloudflare API base URL from https://api.cloudflare.com/#getting-started-endpoints.
const CLOUDFLARE_ENDPOINT = "https://api.cloudflare.com/client/v4/"

// CloudflareAPI config object holding all relevant fields to use the API
type CloudflareAPI struct {
	Log             logr.Logger
	TunnelName      string
	TunnelId        string
	AccountName     string
	AccountId       string
	Domain          string
	APIToken        string
	APIKey          string
	APIEmail        string
	ValidAccountId  string
	ValidTunnelId   string
	ValidTunnelName string
	ValidZoneId     string
}

// CloudflareAPIResponse object containing Result with a Name and Id field (includes an optional CredentialsFile for Tunnel responses)
type CloudflareAPIResponse struct {
	Result struct {
		Id              string
		Name            string
		CredentialsFile map[string]string `json:"credentials_file"`
	}
	Success bool
	Errors  []struct {
		Message string
	}
}

// CloudflareAPIMultiResponse object containing a slice of Results with a Name and Id field
type CloudflareAPIMultiResponse struct {
	Result []struct {
		Id   string
		Name string
	}
	Errors []struct {
		Message string
	}
	Success bool
}

// CloudflareAPITunnelCreate object containing Cloudflare API Input for creating a Tunnel
type CloudflareAPITunnelCreate struct {
	Name         string
	TunnelSecret string `json:"tunnel_secret"`
}

func (c CloudflareAPI) addAuthHeader(req *http.Request, delete bool) error {
	if !delete && c.APIToken != "" {
		req.Header.Add("Authorization", "Bearer "+c.APIToken)
		return nil
	}
	c.Log.Info("No API token, or performing delete operation")
	if c.APIKey == "" || c.APIEmail == "" {
		err := fmt.Errorf("apiKey or apiEmail not found")
		c.Log.Error(err, "Trying to perform Delete request, or any other request with out APIToken, cannot find API Key or Email")
		return err
	}
	req.Header.Add("X-Auth-Key", c.APIKey)
	req.Header.Add("X-Auth-Email", c.APIEmail)
	return nil
}

// CreateCloudflareTunnel creates a Cloudflare Tunnel and returns the tunnel Id and credentials file
func (c *CloudflareAPI) CreateCloudflareTunnel() (string, string, error) {
	if _, err := c.GetAccountId(); err != nil {
		c.Log.Error(err, "error code in getting account ID")
		return "", "", err
	}

	// Generate 32 byte random string for tunnel secret
	randSecret := make([]byte, 32)
	if _, err := rand.Read(randSecret); err != nil {
		return "", "", err
	}
	tunnelSecret := base64.StdEncoding.EncodeToString(randSecret)

	// Generate body for POST request
	postBody, _ := json.Marshal(map[string]string{
		"name":          c.TunnelName,
		"tunnel_secret": tunnelSecret,
	})
	reqBody := bytes.NewBuffer(postBody)

	req, _ := http.NewRequest("POST", CLOUDFLARE_ENDPOINT+"accounts/"+c.ValidAccountId+"/tunnels", reqBody)
	if err := c.addAuthHeader(req, false); err != nil {
		return "", "", err
	}
	req.Header.Add("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.Log.Error(err, "error code in creating tunnel")
		return "", "", err
	}

	defer resp.Body.Close()

	var tunnelResponse CloudflareAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&tunnelResponse); err != nil {
		c.Log.Error(err, "could not read body in creating tunnel")
		return "", "", err
	}

	if !tunnelResponse.Success {
		err := fmt.Errorf("%v", tunnelResponse.Errors)
		c.Log.Error(err, "received error in creating tunnel")
		return "", "", err
	}

	c.ValidTunnelId = tunnelResponse.Result.Id
	c.ValidTunnelName = tunnelResponse.Result.Name

	// Read credentials section and marshal to string
	creds, _ := json.Marshal(tunnelResponse.Result.CredentialsFile)
	return tunnelResponse.Result.Id, string(creds), nil
}

// DeleteCloudflareTunnel deletes a Cloudflare Tunnel
func (c *CloudflareAPI) DeleteCloudflareTunnel() error {
	if err := c.ValidateAll(); err != nil {
		c.Log.Error(err, "Error in validation")
		return err
	}

	req, _ := http.NewRequest("DELETE", CLOUDFLARE_ENDPOINT+"accounts/"+c.ValidAccountId+"/tunnels/"+url.QueryEscape(c.ValidTunnelId), nil)
	if err := c.addAuthHeader(req, true); err != nil {
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.Log.Error(err, "error code in deleting tunnel", "tunnelId", c.TunnelId)
		return err
	}

	defer resp.Body.Close()
	var tunnelResponse CloudflareAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&tunnelResponse); err != nil {
		c.Log.Error(err, "could not read body in deleting tunnel", "tunnelId", c.TunnelId)
		return err
	}

	if !tunnelResponse.Success {
		c.Log.Error(err, "failed to delete tunnel", "tunnelId", c.TunnelId, "tunnelResponse", tunnelResponse)
		return err
	}

	return nil
}

// ValidateAll validates the contents of the CloudflareAPI struct
func (c *CloudflareAPI) ValidateAll() error {
	c.Log.Info("In validation")
	if _, err := c.GetAccountId(); err != nil {
		return err
	}

	if _, err := c.GetTunnelId(); err != nil {
		return err
	}

	if _, err := c.GetZoneId(); err != nil {
		return err
	}

	c.Log.Info("Validation successful")
	return nil
}

// GetAccountId gets AccountId from Account Name
func (c *CloudflareAPI) GetAccountId() (string, error) {
	if c.ValidAccountId != "" {
		return c.ValidAccountId, nil
	}

	if c.AccountId == "" && c.AccountName == "" {
		err := fmt.Errorf("both account ID and Name cannot be empty")
		c.Log.Error(err, "Both accountId and accountName cannot be empty")
		return "", err
	}

	if c.validateAccountId() {
		c.ValidAccountId = c.AccountId
	} else {
		c.Log.Info("Account ID failed, falling back to Account Name")
		accountIdFromName, err := c.getAccountIdByName()
		if err != nil {
			return "", fmt.Errorf("error fetching Account ID by Account Name")
		}
		c.ValidAccountId = accountIdFromName
	}
	return c.ValidAccountId, nil
}

func (c CloudflareAPI) validateAccountId() bool {
	if c.AccountId == "" {
		c.Log.Info("Account ID not provided")
		return false
	}
	req, _ := http.NewRequest("GET", CLOUDFLARE_ENDPOINT+"accounts/"+url.QueryEscape(c.AccountId), nil)
	if err := c.addAuthHeader(req, false); err != nil {
		return false
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.Log.Error(err, "error code in getting account by Account ID", "accountId", c.AccountId)
		return false
	}

	defer resp.Body.Close()
	var accountResponse CloudflareAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&accountResponse); err != nil {
		c.Log.Error(err, "could not read body in getting account by Account ID", "accountId", c.AccountId)
		return false
	}

	return accountResponse.Success && accountResponse.Result.Id == c.AccountId
}

func (c *CloudflareAPI) getAccountIdByName() (string, error) {
	req, _ := http.NewRequest("GET", CLOUDFLARE_ENDPOINT+"accounts?name="+url.QueryEscape(c.AccountName), nil)
	if err := c.addAuthHeader(req, false); err != nil {
		return "", err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.Log.Error(err, "error code in getting account, check accountName", "accountName", c.AccountName)
		return "", err
	}

	defer resp.Body.Close()
	var accountResponse CloudflareAPIMultiResponse
	if err := json.NewDecoder(resp.Body).Decode(&accountResponse); err != nil || !accountResponse.Success {
		c.Log.Error(err, "could not read body in getting account, check accountName", "accountName", c.AccountName)
		return "", err
	}

	switch len(accountResponse.Result) {
	case 0:
		err := fmt.Errorf("no account in response")
		c.Log.Error(err, "found no account, check accountName", "accountName", c.AccountName)
		return "", err
	case 1:
		return accountResponse.Result[0].Id, nil
	default:
		err := fmt.Errorf("more than one account in response")
		c.Log.Error(err, "found more than one account, check accountName", "accountName", c.AccountName)
		return "", err
	}
}

// GetTunnelId gets Tunnel Id from available information
func (c *CloudflareAPI) GetTunnelId() (string, error) {
	if c.ValidTunnelId != "" {
		return c.ValidTunnelId, nil
	}

	if c.TunnelId == "" && c.TunnelName == "" {
		err := fmt.Errorf("both tunnel ID and Name cannot be empty")
		c.Log.Error(err, "Both tunnelId and tunnelName cannot be empty")
		return "", err
	}

	if c.validateTunnelId() {
		c.ValidTunnelId = c.TunnelId
		return c.TunnelId, nil
	}

	c.Log.Info("Tunnel ID failed, falling back to Tunnel Name")
	tunnelIdFromName, err := c.getTunnelIdByName()
	if err != nil {
		return "", fmt.Errorf("error fetching Tunnel ID by Tunnel Name")
	}
	c.ValidTunnelId = tunnelIdFromName
	c.ValidTunnelName = c.TunnelName

	return c.ValidTunnelId, nil
}

func (c *CloudflareAPI) validateTunnelId() bool {
	if c.TunnelId == "" {
		c.Log.Info("Tunnel ID not provided")
		return false
	}

	if _, err := c.GetAccountId(); err != nil {
		c.Log.Error(err, "error in getting account ID")
		return false
	}

	req, _ := http.NewRequest("GET", CLOUDFLARE_ENDPOINT+"accounts/"+c.ValidAccountId+"/tunnels/"+url.QueryEscape(c.TunnelId), nil)
	if err := c.addAuthHeader(req, false); err != nil {
		return false
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.Log.Error(err, "error code in getting tunnel by Tunnel ID", "tunnelId", c.TunnelId)
		return false
	}

	defer resp.Body.Close()
	var tunnelResponse CloudflareAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&tunnelResponse); err != nil {
		c.Log.Error(err, "could not read body in getting tunnel by Tunnel ID", "tunnelId", c.TunnelId)
		return false
	}

	c.ValidTunnelName = tunnelResponse.Result.Name

	return tunnelResponse.Success && tunnelResponse.Result.Id == c.TunnelId
}

func (c *CloudflareAPI) getTunnelIdByName() (string, error) {
	if _, err := c.GetAccountId(); err != nil {
		c.Log.Error(err, "error in getting account ID")
		return "", err
	}

	req, _ := http.NewRequest("GET", CLOUDFLARE_ENDPOINT+"accounts/"+c.ValidAccountId+"/tunnels?name="+url.QueryEscape(c.TunnelName), nil)
	if err := c.addAuthHeader(req, false); err != nil {
		return "", err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.Log.Error(err, "error code in getting tunnel, check tunnelName", "tunnelName", c.TunnelName)
		return "", err
	}

	defer resp.Body.Close()
	var tunnelResponse CloudflareAPIMultiResponse
	if err := json.NewDecoder(resp.Body).Decode(&tunnelResponse); err != nil || !tunnelResponse.Success {
		c.Log.Error(err, "could not read body in getting tunnel, check tunnelName", "tunnelName", c.TunnelName)
		return "", err
	}

	switch len(tunnelResponse.Result) {
	case 0:
		err := fmt.Errorf("no tunnel in response")
		c.Log.Error(err, "found no tunnel, check tunnelName", "tunnelName", c.TunnelName)
		return "", err
	case 1:
		c.ValidTunnelName = tunnelResponse.Result[0].Name
		return tunnelResponse.Result[0].Id, nil
	default:
		err := fmt.Errorf("more than one tunnel in response")
		c.Log.Error(err, "found more than one tunnel, check tunnelName", "tunnelName", c.TunnelName)
		return "", err
	}
}

// GetTunnelCreds gets Tunnel Credentials from Tunnel secret
func (c *CloudflareAPI) GetTunnelCreds(tunnelSecret string) (string, error) {
	if _, err := c.GetAccountId(); err != nil {
		c.Log.Error(err, "error in getting account ID")
		return "", err
	}

	if _, err := c.GetTunnelId(); err != nil {
		c.Log.Error(err, "error in getting tunnel ID")
		return "", err
	}

	creds, err := json.Marshal(map[string]string{
		"AccountTag":   c.ValidAccountId,
		"TunnelSecret": tunnelSecret,
		"TunnelID":     c.ValidTunnelId,
		"TunnelName":   c.ValidTunnelName,
	})

	return string(creds), err
}

// GetZoneId gets Zone Id from DNS domain
func (c *CloudflareAPI) GetZoneId() (string, error) {
	if c.ValidZoneId != "" {
		return c.ValidZoneId, nil
	}

	if c.Domain == "" {
		err := fmt.Errorf("domain cannot be empty")
		c.Log.Error(err, "Domain cannot be empty")
		return "", err
	}

	zoneIdFromName, err := c.getZoneIdByName()
	if err != nil {
		return "", fmt.Errorf("error fetching Zone ID by Zone Name")
	}
	c.ValidZoneId = zoneIdFromName
	return c.ValidZoneId, nil
}

func (c *CloudflareAPI) getZoneIdByName() (string, error) {
	req, _ := http.NewRequest("GET", CLOUDFLARE_ENDPOINT+"zones/?name="+url.QueryEscape(c.Domain), nil)
	if err := c.addAuthHeader(req, false); err != nil {
		return "", err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.Log.Error(err, "error code in getting zoneId, check domain", "domain", c.Domain)
		return "", err
	}

	defer resp.Body.Close()
	var zoneResponse CloudflareAPIMultiResponse
	if err := json.NewDecoder(resp.Body).Decode(&zoneResponse); err != nil || !zoneResponse.Success {
		c.Log.Error(err, "could not read body in getting zoneId, check domain", "domain", c.Domain)
		return "", err
	}

	switch len(zoneResponse.Result) {
	case 0:
		err := fmt.Errorf("no zone in response")
		c.Log.Error(err, "found no zone, check domain", "domain", c.Domain, "zoneResponse", zoneResponse)
		return "", err
	case 1:
		return zoneResponse.Result[0].Id, nil
	default:
		err := fmt.Errorf("more than one zone in response")
		c.Log.Error(err, "found more than one zone, check domain", "domain", c.Domain)
		return "", err
	}
}

// InsertOrUpdateCName upsert DNS CNAME record for the given FQDN to point to the tunnel
func (c *CloudflareAPI) InsertOrUpdateCName(fqdn string) error {
	method := "POST"
	subPath := ""
	if dnsId, err := c.getDNSCNameId(fqdn); err == nil {
		c.Log.Info("Updating existing record", "fqdn", fqdn, "dnsId", dnsId)
		method = "PUT"
		subPath = "/" + dnsId
	} else {
		c.Log.Info("Inserting DNS record", "fqdn", fqdn)
	}

	// Generate body for POST/PUT request
	body, _ := json.Marshal(struct {
		Type    string
		Name    string
		Content string
		Ttl     int
		Proxied bool
	}{
		Type:    "CNAME",
		Name:    fqdn,
		Content: c.ValidTunnelId + ".cfargotunnel.com",
		Ttl:     1,    // Automatic TTL
		Proxied: true, // For Cloudflare tunnels
	})
	reqBody := bytes.NewBuffer(body)

	req, _ := http.NewRequest(method, CLOUDFLARE_ENDPOINT+"zones/"+c.ValidZoneId+"/dns_records"+subPath, reqBody)
	if err := c.addAuthHeader(req, false); err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.Log.Error(err, "error code in setting/updating DNS record, check fqdn", "fqdn", fqdn)
		return err
	}

	defer resp.Body.Close()
	var dnsResponse CloudflareAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&dnsResponse); err != nil || !dnsResponse.Success {
		c.Log.Error(err, "could not read body in setting DNS record", "response", dnsResponse)
		return err
	}
	c.Log.Info("DNS record set successful", "fqdn", fqdn)
	return nil
}

// DeleteDNSCName deletes DNS CNAME entry for the given FQDN
func (c *CloudflareAPI) DeleteDNSCName(fqdn string) error {
	dnsId, err := c.getDNSCNameId(fqdn)
	if err != nil {
		c.Log.Info("Cannot find DNS record, already deleted", "fqdn", fqdn)
		return nil
	}

	req, _ := http.NewRequest("DELETE", CLOUDFLARE_ENDPOINT+"zones/"+c.ValidZoneId+"/dns_records/"+dnsId, nil)
	if err := c.addAuthHeader(req, false); err != nil {
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.Log.Error(err, "error code in deleting DNS record, check fqdn", "dnsId", dnsId, "fqdn", fqdn)
		return err
	}

	defer resp.Body.Close()
	var dnsResponse struct {
		Result struct {
			Id string
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&dnsResponse); err != nil || dnsResponse.Result.Id != dnsId {
		c.Log.Error(err, "could not read body in deleting DNS record", "fqdn", fqdn, "dnsId", dnsId, "response", dnsResponse)
		return err
	}
	return nil
}

func (c *CloudflareAPI) getDNSCNameId(fqdn string) (string, error) {
	if _, err := c.GetZoneId(); err != nil {
		c.Log.Error(err, "error in getting Zone ID")
		return "", err
	}

	req, _ := http.NewRequest("GET", CLOUDFLARE_ENDPOINT+"zones/"+c.ValidZoneId+"/dns_records?type=CNAME&name="+url.QueryEscape(fqdn), nil)
	if err := c.addAuthHeader(req, false); err != nil {
		return "", err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.Log.Error(err, "error code in getting DNS record, check fqdn", "fqdn", fqdn)
		return "", err
	}

	defer resp.Body.Close()
	var dnsResponse CloudflareAPIMultiResponse
	if err := json.NewDecoder(resp.Body).Decode(&dnsResponse); err != nil || !dnsResponse.Success {
		c.Log.Error(err, "could not read body in getting zoneId, check domain", "domain", c.Domain)
		return "", err
	}

	if len(dnsResponse.Result) == 0 {
		err := fmt.Errorf("no records returned")
		c.Log.Info("no records returned for fqdn", "fqdn", fqdn)
		return "", err
	}

	if len(dnsResponse.Result) > 1 {
		err := fmt.Errorf("multiple records returned")
		c.Log.Error(err, "multiple records returned for fqdn", "fqdn", fqdn)
		return "", err
	}

	return dnsResponse.Result[0].Id, nil
}
