package multipartHTTP

import (
	"net/http"
	"strings"

	"github.com/grafana/sobek"
	"go.k6.io/k6/lib"
)

type httpOpenArgs struct {
	// setupFn sobek.Callable
	headers http.Header
	method  string
	body    string
	// cookieJar   *cookiejar.Jar
	// tagsAndMeta *metrics.TagsAndMeta
}

func parseConnectArgs(state *lib.State, rt *sobek.Runtime, args ...sobek.Value) (*httpOpenArgs, error) {
	var paramsV = args[0]
	// The params argument is optional
	// var callableV, paramsV sobek.Value
	// switch len(args) {
	// case 2:
	// 	paramsV = args[0]
	// 	callableV = args[1]
	// case 1:
	// 	paramsV = sobek.Undefined()
	// 	callableV = args[0]
	// default:
	// 	return nil, errors.New("invalid number of arguments to sse.open")
	// }
	// Get the callable (required)
	// setupFn, isFunc := sobek.AssertFunction(callableV)
	// if !isFunc {
	// 	return nil, errors.New("last argument to sse.open must be a function")
	// }

	headers := make(http.Header)
	headers.Set("User-Agent", state.Options.UserAgent.String)
	parsedArgs := &httpOpenArgs{
		// setupFn: setupFn,
		headers: headers,
	}

	if sobek.IsUndefined(paramsV) || sobek.IsNull(paramsV) {
		return parsedArgs, nil
	}

	// Parse the optional second argument (params)
	params := paramsV.ToObject(rt)
	for _, k := range params.Keys() {
		switch k {
		case "headers":
			headersV := params.Get(k)
			if sobek.IsUndefined(headersV) || sobek.IsNull(headersV) {
				continue
			}
			headersObj := headersV.ToObject(rt)
			if headersObj == nil {
				continue
			}
			for _, key := range headersObj.Keys() {
				parsedArgs.headers.Set(key, headersObj.Get(key).String())
			}
		case "method":
			parsedArgs.method = strings.TrimSpace(params.Get(k).ToString().String())
		case "body":
			parsedArgs.body = strings.TrimSpace(params.Get(k).ToString().String())
		}
	}

	return parsedArgs, nil
}
