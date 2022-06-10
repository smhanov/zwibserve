package zwibserve

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
)

// HTTPRequestArgs is the parameters for the request
type HTTPRequestArgs struct {
	// GET, POST, ETC
	Method string

	URI string

	Headers map[string]string

	Data map[string]interface{}

	Body []byte

	// If JSON is to be sent in the post request
	JSON interface{}

	// If basic authentication is used
	Username string
	Password string
}

// HTTPRequestResult contains the meta information about the reply.
type HTTPRequestResult struct {
	Err        error
	StatusCode int
	Header     http.Header
	RawReply   []byte
}

// MakeHTTPRequest makes an HTTP request
func MakeHTTPRequest(args HTTPRequestArgs, reply interface{}) HTTPRequestResult {

	var body io.Reader
	var length int
	var result HTTPRequestResult

	uri := args.URI

	if args.JSON != nil {
		b, err := json.Marshal(args.JSON)
		if err != nil {
			result.Err = err
			return result
		}

		length = len(b)
		body = bytes.NewReader(b)
	}

	if args.Data != nil {
		data := url.Values{}
		if args.Data != nil {
			for name, value := range args.Data {
				if values, ok := value.([]string); ok {
					for _, item := range values {
						data.Add(name, item)
					}
				} else {
					data.Set(name, fmt.Sprintf("%v", value))
				}
			}

			if args.Method != "POST" {
				uri += "?" + data.Encode()
			} else {
				b := data.Encode()
				length = len(b)
				body = bytes.NewReader([]byte(b))
			}
		}
	}

	log.Printf("uri %s", uri)

	if args.Body != nil {
		body = bytes.NewReader(args.Body)
		length = len(args.Body)
	}

	req, _ := http.NewRequest(args.Method, uri, body)

	client := &http.Client{}

	// nil map is like empty on reading
	for key, value := range args.Headers {
		req.Header.Add(key, value)
	}

	if args.Username != "" {
		userpass := args.Username + ":" + args.Password
		sEnc := base64.StdEncoding.EncodeToString([]byte(userpass))
		req.Header.Add("Authorization", "Basic "+sEnc)
	}

	if args.JSON != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if args.Method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Content-Length", fmt.Sprintf("%d", length))
	}

	//log.Printf("request is %v", req)

	resp, err := client.Do(req)
	if err != nil {
		result.Err = err
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Header = resp.Header

	responseData, err := ioutil.ReadAll(resp.Body)
	//log.Printf("Got response %s", string(responseData))
	if err != nil {
		result.Err = err
		return result
	}

	if strings.Contains(resp.Header.Get("Content-Type"), "application/json") {

		err = json.Unmarshal(responseData, reply)
		if err != nil {
			result.Err = err
			return result
		}
	} else {
		switch v := reply.(type) {
		case *string:
			*v = string(responseData)
		case *[]byte:
			*v = responseData
		default:
			log.Printf("Content-type: %s", resp.Header.Get("Content-Type"))
			log.Printf("Response: %v", string(responseData))
			log.Panic(fmt.Errorf("invalid reply type; need string/byte"))
		}
	}
	result.RawReply = responseData

	return result
}

// MakeAsyncHTTPRequest makes an asyncronous http request, returning it result in a channel.
func MakeAsyncHTTPRequest(args HTTPRequestArgs, reply interface{}) chan HTTPRequestResult {
	ch := make(chan HTTPRequestResult)

	go func() {
		ch <- MakeHTTPRequest(args, reply)
	}()

	return ch
}

// RecoverErrors will wrap an HTTP handler. When a panic occurs, it will
// print the stack to the log. Secondly, it will return the internal server error
// with the status header equal to the error string.
func RecoverErrors(fn http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if thing := recover(); thing != nil {
				code := http.StatusInternalServerError
				status := "Internal server error"
				switch v := thing.(type) {
				case HTTPError:
					code = v.StatusCode()
					status = v.Error()
				default:
					status = fmt.Sprintf("%v", thing)
					log.Printf("%v", thing)
					log.Println(string(debug.Stack()))
				}
				w.Header().Set("Status", status)
				w.WriteHeader(code)
			}
		}()

		fn.ServeHTTP(w, r)
	}
}

type HTTPError interface {
	Error() string
	StatusCode() int
}

type httpError struct {
	status  int
	message string
}

func (h httpError) Error() string {
	return h.message
}

func (h httpError) StatusCode() int {
	return h.status
}

// HTTPPanic will cause a panic with an HTTPError. This is expected to be
// recovered at a higher level, for example using the RecoverErrors
// middleware so the error is returned to the client.
func HTTPPanic(status int, fmtStr string, args ...interface{}) HTTPError {
	panic(httpError{status, fmt.Sprintf(fmtStr, args...)})
}

// CORS wraps an HTTP request handler, adding appropriate cors headers.
// If CORS is desired, you can wrap the handler with it.
func CORS(fn http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers",
				"Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
			w.Header().Set("Access-Control-Expose-Headers", "Status, Content-Type, Content-Length")
		}
		// Stop here if its Preflighted OPTIONS request
		if r.Method == "OPTIONS" {
			return
		}

		fn.ServeHTTP(w, r)
	}
}
