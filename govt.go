/*
govt is a VirusTotal API v2 client written for the Go programming language.

Written by Willi Ballenthin while at Mandiant.
June, 2013.

File upload capabilities by Florian 'scusi' Walther
June, 2014.

File distribution support by Christopher 'tankbusta' Schmitt while at Mandiant
October, 2014.
*/
package govt

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"github.com/Spivaka/filteredError"
)

const (
	DefaultURL = "https://www.virustotal.com/vtapi/v2/"
)

// Client interacts with the services provided by VirusTotal.
type Client struct {
	apikey            string       // private API key
	url               string       // VT URL, probably ends with .../v2/. Must end in '/'.
	basicAuthUsername string       // Optional username for BasicAuth on VT proxy.
	basicAuthPassword string       // Optional password for BasicAuth on VT proxy.
	errorlog          *log.Logger  // Optional logger to write errors to
	tracelog          *log.Logger  // Optional logger to write trace and debug data to
	c                 *http.Client // The client to use for requests
}

// Status is the set of fields shared among all VT responses.
type Status struct {
	ResponseCode int    `json:"response_code"`
	VerboseMsg   string `json:"verbose_msg"`
}

// FileResult
type FileDownloadResult struct {
	Content []byte
}

// FileScan is defined by VT.
type FileScan struct {
	Detected bool   `json:"detected"`
	Version  string `json:"version"`
	Result   string `json:"result"`
	Update   string `json:"update"`
}

type FileReportDistrib struct {
	Md5           string `json:"md5"`
	Sha1          string `json:"sha1"`
	Sha256        string `json:"sha256"`
	Type          string `json:"type"`
	FirstSeen     string `json:"first_seen"`
	LastSeen      string `json:"last_seen"`
	Link          string `json:"link"`
	Name          string `json:"name"`
	Size          int    `json:"size"`
	SourceCountry string `json:"source_country"`
	SourceId      string `json:"source_id"`
	Timestamp     int    `json:"timestamp"`
	VHash         string `json:"vhash"`
	// Ugh. VT inconsistency. Data is an array rather than k/v like other APIs
	Scans map[string][]string `json:"report"`
}

type FileDistributionResults []FileReportDistrib

// FileReport is defined by VT.
type FileReport struct {
	Status
	Resource  string              `json:"resource"`
	ScanId    string              `json:"scan_id"`
	Md5       string              `json:"md5"`
	Sha1      string              `json:"sha1"`
	Sha256    string              `json:"sha256"`
	ScanDate  string              `json:"scan_date"`
	Positives uint16              `json:"positives"`
	Total     uint16              `json:"total"`
	Scans     map[string]FileScan `json:"scans"`
	Permalink string              `json:"permalink"`
}

// ScanFileResult is defined by VT.
type ScanFileResult struct {
	Status
	Resource  string `json:"resource"`
	ScanId    string `json:"scan_id"`
	Permalink string `json:"permalink"`
	Sha256    string `json:"sha256"`
	Sha1      string `json:"sha1"`
	Md5       string `json:"md5"`
}

// FileReportResults is defined by VT.
type FileReportResults []FileReport

// RescanFileResult is defined by VT.
type RescanFileResult struct {
	Status
	Resource  string `json:"resource"`
	ScanId    string `json:"scan_id"`
	Permalink string `json:"permalink"`
	Sha256    string `json:"sha256"`
}

// RescanFileResults is defined by VT.
type RescanFileResults []RescanFileResult

// ScanUrlResult is defined by VT.
type ScanUrlResult struct {
	Status
	ScanId    string `json:"scan_id"`
	ScanDate  string `json:"scan_date"`
	Permalink string `json:"permalink"`
	Url       string `json:"url"`
}

// UrlScan is defined by VT.
type UrlScan struct {
	Detected bool   `json:"detected"`
	Result   string `json:"result"`
}

// UrlReport is defined by VT.
type UrlReport struct {
	Status
	Url        string             `json:"url"`
	Resource   string             `json:"resource"`
	ScanId     string             `json:"scan_id"`
	ScanDate   string             `json:"scan_date"`
	Permalink  string             `json:"permalink"`
	Positives  uint16             `json:"positives"`
	Total      uint16             `json:"total"`
	Scans      map[string]UrlScan `json:"scans"`
	FileScanId string             `json:"filescan_id"`
}

// UrlReports is defined by VT.
type UrlReports []UrlReport

// ScanUrlResults is defined by VT.
type ScanUrlResults []ScanUrlResult

// IpResolution is defined by VT.
type IpResolution struct {
	LastResolved string `json:"last_resolved"`
	Hostname     string `json:"hostname"`
}

// DetectedUrl is defined by VT.
type DetectedUrl struct {
	Url       string `json:"url"`
	Total     uint16 `json:"total"`
	Positives uint16 `json:"positives"`
	ScanDate  string `json:"scan_date"`
}

// IpReport is defined by VT.
type IpReport struct {
	Status
	Resolutions  []IpResolution
	DetectedUrls []DetectedUrl `json:"detected_urls"`
}

// DomainResolution is defined by VT.
type DomainResolution struct {
	LastResolved string `json:"last_resolved"`
	IpAddress    string `json:"ip_address"`
}

// DomainReport is defined by VT.
type DomainReport struct {
	Status
	Resolutions  []DomainResolution
	DetectedUrls []DetectedUrl `json:"detected_urls"`
}

// ClientError is a generic error specific to the `govt` package.
type ClientError struct {
	msg string
}

// Error returns a string representation of the error condition.
func (self ClientError) Error() string {
	return self.msg
}

// OptionFunc is a function that configures a Client.
// It is used in New
type OptionFunc func(*Client) error

// errorf logs to the error log.
func (self *Client) errorf(format string, args ...interface{}) {
	if self.errorlog != nil {
		self.errorlog.Printf(format, args...)
	}
}

// tracef logs to the trace log.
func (self *Client) tracef(format string, args ...interface{}) {
	if self.tracelog != nil {
		self.tracelog.Printf(format, args...)
	}
}

// New creates a new virustotal client.
//
// The caller can configure the new client by passing configuration options to the func.
//
// Example:
//
//   client, err := govt.New(
//     govt.SetUrl("http://some.url.com:port"),
//     govt.SetErrorLog(log.New(os.Stderr, "VT: ", log.Lshortfile))
//
// If no URL is configured, Client uses DefaultURL by default.
//
// If no HttpClient is configured, then http.DefaultClient is used.
// You can use your own http.Client with some http.Transport for advanced scenarios.
//
// An error is also returned when some configuration option is invalid.
func New(options ...OptionFunc) (*Client, error) {
	// Set up the client
	c := &Client{
		url: "",
		c:   http.DefaultClient,
	}

	// Run the options on it
	for _, option := range options {
		if err := option(c); err != nil {
			return nil, err
		}
	}
	if c.apikey == "" {
		msg := "No API key specified"
		c.errorf(msg)
		return nil, ClientError{msg: msg}
	}
	if c.url == "" {
		c.url = DefaultURL
	}
	if !strings.HasSuffix(c.url, "/") {
		c.url += "/"
	}
	c.tracef("Using URL [%s]\n", c.url)

	return c, nil
}

// Initialization functions

// SetApikey sets the VT API key to use
func SetApikey(apikey string) OptionFunc {
	return func(self *Client) error {
		if apikey == "" {
			msg := "You must provide an API key to use the client"
			self.errorf(msg)
			return ClientError{msg: msg}
		}
		self.apikey = apikey
		return nil
	}
}

// SetHttpClient can be used to specify the http.Client to use when making
// HTTP requests to VT.
func SetHttpClient(httpClient *http.Client) OptionFunc {
	return func(self *Client) error {
		if httpClient != nil {
			self.c = httpClient
		} else {
			self.c = http.DefaultClient
		}
		return nil
	}
}

// SetUrl defines the URL endpoint VT
func SetUrl(rawurl string) OptionFunc {
	return func(self *Client) error {
		if rawurl == "" {
			rawurl = DefaultURL
		}
		u, err := url.Parse(rawurl)
		if err != nil {
			self.errorf("Invalid URL [%s] - %v\n", rawurl, err)
			return err
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			msg := fmt.Sprintf("Invalid schema specified [%s]", rawurl)
			self.errorf(msg)
			return ClientError{msg: msg}
		}
		self.url = rawurl
		return nil
	}
}

// SetBasicAuth allows to set proxy credentials
func SetBasicAuth(username, password string) OptionFunc {
	return func(self *Client) error {
		self.basicAuthUsername = username
		self.basicAuthPassword = password
		return nil
	}
}

// SetErrorLog sets the logger for critical messages. It is nil by default.
func SetErrorLog(logger *log.Logger) func(*Client) error {
	return func(c *Client) error {
		c.errorlog = logger
		return nil
	}
}

// SetTraceLog specifies the logger to use for output of trace messages like
// HTTP requests and responses. It is nil by default.
func SetTraceLog(logger *log.Logger) func(*Client) error {
	return func(c *Client) error {
		c.tracelog = logger
		return nil
	}
}

// dumpRequest dumps a request to the debug logger if it was defined
func (self *Client) dumpRequest(req *http.Request) {
	if self.tracelog != nil {
		out, err := httputil.DumpRequestOut(req, true)
		if err == nil {
			self.tracef("%s\n", string(out))
		}
	}
}

// dumpResponse dumps a response to the debug logger if it was defined
func (self *Client) dumpResponse(resp *http.Response) {
	if self.tracelog != nil {
		out, err := httputil.DumpResponse(resp, true)
		if err == nil {
			self.tracef("%s\n", string(out))
		}
	}
}

// Request handling functions

// handleError will handle responses with status code different from 200
func (self *Client) handleError(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK {
		if self.errorlog != nil {
			out, err := httputil.DumpResponse(resp, true)
			if err == nil {
				self.errorf("%s\n", string(out))
			}
		}
		if resp.Body != nil {
			resp.Body.Close()
		}
		msg := fmt.Sprintf("Unexpected status code: %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))
		self.errorf(msg)
		return ClientError{msg: msg}
	}
	return nil
}

// makeApiGetRequest fetches a URL with querystring via HTTP GET and
//  returns the response if the status code is HTTP 200
// `parameters` should not include the apikey.
// The caller must call `resp.Body.Close()`.
func (self *Client) makeApiGetRequest(fullurl string, parameters Parameters) (resp *http.Response, err error) {
	values := url.Values{}
	values.Set("apikey", self.apikey)
	for k, v := range parameters {
		values.Add(k, v)
	}

	// TODO(wb) check if final character is ?, or if ? already exists
	req, err := http.NewRequest("GET", fullurl+"?"+values.Encode(), nil)
	if err != nil {
		return resp, err
	}

	if self.basicAuthUsername != "" {
		req.SetBasicAuth(self.basicAuthUsername, self.basicAuthPassword)
	}
	self.dumpRequest(req)
	resp, err = self.c.Do(req)
	if err != nil {
		return resp, filteredError.NewFilteredError(err, map[string]string{self.apikey: "*****"})
	}

	self.dumpResponse(resp)

	if err = self.handleError(resp); err != nil {
		return resp, err
	}

	return resp, nil
}

// makeApiPostRequest fetches a URL with querystring via HTTP POST and
//  returns the response if the status code is HTTP 200
// `parameters` should not include the apikey.
// The caller must call `resp.Body.Close()`.
func (self *Client) makeApiPostRequest(fullurl string, parameters Parameters) (resp *http.Response, err error) {
	values := url.Values{}
	values.Set("apikey", self.apikey)
	for k, v := range parameters {
		values.Add(k, v)
	}

	resp, err = http.PostForm(fullurl, values)
	if err != nil {
		return resp, err
	}

	self.dumpResponse(resp)

	if err = self.handleError(resp); err != nil {
		return resp, err
	}

	return resp, nil
}

// makeApiUploadRequest uploads a file via multipart/mime POST and
//  returns the response if the status code is HTTP 200
// `parameters` should not include the apikey.
// The caller must call `resp.Body.Close()`.
func (self *Client) makeApiUploadRequest(fullurl string, parameters Parameters, paramName, path string) (resp *http.Response, err error) {
	// open the file
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	// set Apikey as parameter
	parameters["apikey"] = self.apikey
	// Pipe the file so as not to read it into memory
	bodyReader, bodyWriter := io.Pipe()
	// create a multipat/mime writer
	writer := multipart.NewWriter(bodyWriter)
	// get the Content-Type of our form data
	fdct := writer.FormDataContentType()
	// Read file errors from the channel
	errChan := make(chan error, 1)
	go func() {
		defer bodyWriter.Close()
		defer file.Close()
		part, err := writer.CreateFormFile(paramName, filepath.Base(path))
		if err != nil {
			errChan <- err
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			errChan <- err
			return
		}
		for k, v := range parameters {
			if err := writer.WriteField(k, v); err != nil {
				errChan <- err
				return
			}
		}
		errChan <- writer.Close()
	}()

	// create a HTTP request with our body, that contains our file
	postReq, err := http.NewRequest("POST", fullurl, bodyReader)
	if err != nil {
		return resp, err
	}
	// add the Content-Type we got earlier to the request header.
	//  some implementations fail if this is not present. (malwr.com, virustotal.com, probably others too)
	//  this could also be a bug in go actually.
	postReq.Header.Add("Content-Type", fdct)

	self.dumpRequest(postReq)

	// send our request off, get response and/or error
	resp, err = self.c.Do(postReq)
	if cerr := <-errChan; cerr != nil {
		return resp, cerr
	}
	if err != nil {
		return resp, filteredError.NewFilteredError(err, map[string]string{self.apikey: "*****"})
	}

	self.dumpResponse(resp)

	if err = self.handleError(resp); err != nil {
		return resp, err
	}
	// we made it, let's return
	return resp, nil
}

type Parameters map[string]string

// fetchApiJson makes a request to the API and decodes the response.
// `method` is one of "GET", "POST", or "FILE"
// `actionurl` is the final path component that specifies the API call
// `parameters` does not include the API key
// `result` is modified as an output parameter. It must be a pointer to a VT JSON structure.
func (self *Client) fetchApiJson(method string, actionurl string, parameters Parameters, result interface{}) (err error) {
	theurl := self.url + actionurl
	var resp *http.Response
	switch method {
	case "GET":
		resp, err = self.makeApiGetRequest(theurl, parameters)
	case "POST":
		resp, err = self.makeApiPostRequest(theurl, parameters)
	case "FILE":
		// get the path to our file from parameters["filename"]
		path := parameters["filename"]
		// call makeApiUploadRequest with fresh/empty Parameters
		resp, err = self.makeApiUploadRequest(theurl, Parameters{}, "file", path)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	if err = dec.Decode(result); err != nil {
		return err
	}

	return nil
}

// fetchApiFile makes a get request to the API and returns the file content
func (self *Client) fetchApiFile(actionurl string, parameters Parameters) (data []byte, err error) {
	theurl := self.url + actionurl
	var resp *http.Response
	resp, err = self.makeApiGetRequest(theurl, parameters)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Public API

// ScanUrl asks VT to redo analysis on the specified file.
func (self *Client) ScanUrl(url string) (r *ScanUrlResult, err error) {
	r = &ScanUrlResult{}
	err = self.fetchApiJson("POST", "url/scan", Parameters{"url": url}, r)
	return r, err
}

// ScanUrls asks VT to redo analysis on the specified files.
func (self *Client) ScanUrls(urls []string) (r *ScanUrlResults, err error) {
	r = &ScanUrlResults{}
	parameters := Parameters{"resource": strings.Join(urls, "\n")}
	err = self.fetchApiJson("POST", "url/scan", parameters, r)
	return r, err
}

// ScanFile asks VT to analysis on the specified file, thats also uploaded.
func (self *Client) ScanFile(file string) (r *ScanFileResult, err error) {
	r = &ScanFileResult{}
	// HACK: here i misuse fetchApiJson a bit,
	//  introduced a new "method" called 'File',
	//  which will make fetchApiJson to invoke makeApiUploadRequest
	//  instead of makeApiPostRequest.
	//
	//  i use Parameters map to pass the filename to fetchApiJson, which
	//  in turn drops the map and calls makeApiUploadRequest with a fresh one
	err = self.fetchApiJson("FILE", "file/scan", Parameters{"filename": file}, r)
	return r, err
}

// RescanFile asks VT to redo analysis on the specified file.
func (self *Client) RescanFile(md5 string) (r *RescanFileResult, err error) {
	r = &RescanFileResult{}
	err = self.fetchApiJson("POST", "file/rescan", Parameters{"resource": md5}, r)
	return r, err
}

// RescanFiles asks VT to redo analysis on the specified files.
func (self *Client) RescanFiles(md5s []string) (r *RescanFileResults, err error) {
	r = &RescanFileResults{}
	parameters := Parameters{"resource": strings.Join(md5s, ",")}
	err = self.fetchApiJson("POST", "file/rescan", parameters, r)
	return r, err
}

// GetFileReport fetches the AV scan reports tracked by VT given an MD5 hash value.
func (self *Client) GetFileReport(md5 string) (r *FileReport, err error) {
	r = &FileReport{}
	err = self.fetchApiJson("GET", "file/report", Parameters{"resource": md5}, r)
	return r, err
}

// GetFileReports fetches the AV scan reports tracked by VT given set of MD5 hash values.
func (self *Client) GetFileReports(md5s []string) (r *FileReportResults, err error) {
	r = &FileReportResults{}
	parameters := Parameters{"resource": strings.Join(md5s, ",")}
	err = self.fetchApiJson("GET", "file/report", parameters, r)
	return r, err
}

// GetFile fetches a file from VT that matches a given md5/sha1/sha256 sum
func (self *Client) GetFile(hash string) (r *FileDownloadResult, err error) {
	r = &FileDownloadResult{}
	parameters := Parameters{"hash": hash}
	data, err := self.fetchApiFile("file/download", parameters)
	r.Content = data
	return r, err
}

func (self *Client) GetFileNetworkTraffic(hash string) (r *FileDownloadResult, err error) {
	r = &FileDownloadResult{}
	parameters := Parameters{"hash": hash}
	data, err := self.fetchApiFile("file/network-traffic", parameters)
	r.Content = data
	return r, err
}

// GetFileDistribution fetches files from the VT distribution API
func (self *Client) GetFileDistribution(params *Parameters) (r *FileDistributionResults, err error) {
	r = &FileDistributionResults{}
	err = self.fetchApiJson("GET", "file/distribution", *params, r)
	return r, err
}

// GetUrlReport fetches the AV scan reports tracked by VT given a URL.
// Does not support the optional `scan` parameter.
func (self *Client) GetUrlReport(url string) (r *UrlReport, err error) {
	r = &UrlReport{}
	err = self.fetchApiJson("POST", "url/report", Parameters{"resource": url}, r)
	return r, err
}

// GetUrlReports fetches AV scan reports tracked by VT given URLs.
// Does not support the optional `scan` parameter.
func (self *Client) GetUrlReports(urls []string) (r *UrlReports, err error) {
	r = &UrlReports{}
	parameters := Parameters{"resource": strings.Join(urls, "\n")}
	err = self.fetchApiJson("POST", "url/report", parameters, r)
	return r, err
}

// GetIpReport fetches the passive DNS information about an IP address.
func (self *Client) GetIpReport(ip string) (r *IpReport, err error) {
	r = &IpReport{}
	err = self.fetchApiJson("GET", "ip-address/report", Parameters{"ip": ip}, r)
	return r, err
}

// GetDomainReport fetches the passive DNS information about a DNS address.
func (self *Client) GetDomainReport(domain string) (r *DomainReport, err error) {
	r = &DomainReport{}
	err = self.fetchApiJson("GET", "domain/report", Parameters{"domain": domain}, r)
	return r, err
}

// MakeComment adds a comment to a file/URL/IP/domain.
func (self *Client) MakeComment(resource string, comment string) (r *Status, err error) {
	r = &Status{}
	parameters := Parameters{"resource": resource, "comment": comment}
	err = self.fetchApiJson("POST", "comments/put", parameters, r)
	return r, err
}
