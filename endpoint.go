package net

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
)

type paramKey int

var pKey paramKey = 2

//READLIMIT read limit
const (
	MB        = 1 << 20
	READLIMIT = MB
	BUFFERMAX = 5 * MB
)

func NewServer() *Server {
	router := httprouter.New()
	router.RedirectTrailingSlash = false
	router.RedirectFixedPath = false
	router.PanicHandler = func(w http.ResponseWriter, r *http.Request, v interface{}) {
		ErrorResponse(w, fmt.Errorf("%+v", v))
	}
	return &Server{
		Router: router,
	}
}

type Server struct {
	*httprouter.Router
}

// ResultResponse json response
func ResultResponse(w http.ResponseWriter, result interface{}) {
	ret := JSONResult{
		StatusCode: http.StatusOK,
		Success:    true,
		Result:     result,
	}
	ret.Write(w)
}

func NoAccess(w http.ResponseWriter) {
	res := JSONResult{
		Success:    false,
		StatusCode: http.StatusUnauthorized,
		Error:      "No Access",
	}
	res.Write(w)
}

// Params get params
func Params(ctx context.Context) (httprouter.Params, error) {
	params, ok := ctx.Value(pKey).(httprouter.Params)
	if !ok {
		return httprouter.Params{}, fmt.Errorf("no params in context")
	}
	return params, nil
}

// ErrorResponse error json response
func ErrorResponse(w http.ResponseWriter, err error) {
	ret := JSONResult{
		StatusCode: http.StatusInternalServerError,
		Success:    false,
		Error:      err.Error(),
	}
	log.Print(err)
	ret.Write(w)
}

func SizeResponse(w http.ResponseWriter, err error) {
	ret := JSONResult{
		StatusCode: http.StatusExpectationFailed,
		Success:    false,
		Error:      err.Error(),
	}
	ret.Write(w)
}

// JSONResult json result struct
type JSONResult struct {
	Success    bool        `json:"success"`
	StatusCode int         `json:"-"`
	Error      string      `json:"error,omitempty"`
	Result     interface{} `json:"result,omitempty"`
}

// Write write jsonresult to output
func (r JSONResult) Write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(r.StatusCode)
	if err := json.NewEncoder(w).Encode(r); err != nil {
		panic(err)
	}
}

// DecodeJSONBody decode posted json body
func DecodeBody(r *http.Request, v interface{}) error {
	body, err := ioutil.ReadAll(io.LimitReader(r.Body, READLIMIT))
	if err != nil {
		return err
	}
	if err := r.Body.Close(); err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return err
	}
	return nil
}

// EndPointDecorator decorates endpoints
type EndPointDecorator func(EndPoint) EndPoint

// EndPointConfig collection of decorators to be applied
type EndPointConfig []EndPointDecorator

// Apply progressive enhancement endpoint
func (ed EndPointConfig) Apply(endpoint EndPoint) EndPoint {
	a := len(ed) - 1
	ep := endpoint
	for ; a >= 0; a-- {
		ep = ed[a](ep)
	}
	return ep
}

func Logger(e EndPoint) EndPoint {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		defer func(begin time.Time) {
			dur := time.Now().Sub(begin)
			log.Printf("request took %d ms\n", dur/time.Millisecond)
		}(time.Now())
		e(ctx, w, r)
	}
}

func TimeOut(e EndPoint) EndPoint {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithDeadline(
			ctx,
			time.Now().Add(50*time.Millisecond),
		)
		defer cancel()
		go func() {
			select {
			case <-ctx.Done():
				log.Printf("error: %s", ctx.Err())
				return
			}
		}()
		e(ctx, w, r)
	}
}

func LimitUp(e EndPoint) EndPoint {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > BUFFERMAX {
			SizeResponse(w, fmt.Errorf(
				"post is too big, probably illegal shit going on",
			))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, BUFFERMAX)
		e(ctx, w, r)
	}
}

func CorsHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		//w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == http.MethodOptions {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			w.Header().Set(
				"Access-Control-Allow-Methods",
				strings.ToUpper(r.Header.Get("Access-Control-Request-Method")),
			)
			w.Header().Set(
				"Access-Control-Allow-Headers",
				"authorization",
			)
			w.WriteHeader(http.StatusOK)
			return

		}
		handler.ServeHTTP(w, r)
	})
}

func Context(ctx context.Context, params httprouter.Params) context.Context {
	return context.WithValue(ctx, pKey, params)
}

// EndPoint http endpoint
type EndPoint func(context.Context, http.ResponseWriter, *http.Request)

// AddEndPoint add endpoint to server
func (s *Server) AddEndPoint(method, path string, endpoint EndPoint) {
	s.Handle(method, path, func(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
		//for now no timeout or cancel funcs
		ctx := Context(req.Context(), p)
		req = req.WithContext(ctx)
		endpoint(ctx, w, req)
	})
}

func ConfigValue(key string) string {
	val, ok := os.LookupEnv(key)
	if !ok {
		panic(fmt.Sprintf("No value for key: %s \n", key))
	}
	return val
}
