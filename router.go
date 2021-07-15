package echo

import (
	"errors"
	"net/http"
	"net/url"
)

// Router is interface for routing requests to registered routes.
type Router interface {
	// Add registers Routable with the Router and returns registered RouteInfo
	Add(routable Routable) (RouteInfo, error)
	// Remove removes route from the Router
	Remove(method string, path string) error
	// Routes returns information about all registered routes
	Routes() Routes

	// Match searches Router for matching route and applies it to result fields.
	Match(req *http.Request, params *PathParams) RouteMatch
}

// Routable is interface for registering Route with Router. During route registration process the Router will
// convert Routable to RouteInfo with ToRouteInfo method. By creating custom implementation of Routable additional
// information about registered route can be stored in Routes (i.e. privileges used with route etc.)
type Routable interface {
	// ToRouteInfo converts Routable to RouteInfo
	//
	// This method is meant to be used by Router after it parses url for path parameters, to store information about
	// route just added.
	ToRouteInfo(params []string) RouteInfo
	// ToRoute converts Routable to Route which Router uses to register the method handler for path.
	//
	// This method is meant to be used by Router to get fields (including handler and middleware functions) needed to
	// add Route to Router.
	ToRoute() Route
	// ForGroup recreates routable with added group prefix and group middlewares it is grouped to.
	//
	// Is necessary for Echo.Group to be able to add/register Routable with Router and having group prefix and group
	// middlewares included in actually registered Route.
	ForGroup(pathPrefix string, middlewares []MiddlewareFunc) Routable
}

// Routes is collection of RouteInfo instances with various helper methods.
type Routes []RouteInfo

// RouteInfo describes registered route base fields.
// Method+Path pair uniquely identifies the Route. Name can have duplicates.
type RouteInfo interface {
	Method() string
	Path() string
	Name() string

	Params() []string
	Reverse(params ...interface{}) string

	// NOTE: handler and middlewares are not exposed because handler could be already wrapping middlewares and therefore
	// it is not always 100% known if handler function already wraps middlewares or not. In Echo handler could be one
	// function or several functions wrapping each other.
}

// RouteMatchType describes possible states that request could be in perspective of routing
type RouteMatchType uint8

const (
	// RouteMatchUnknown is state before routing is done. Default state for fresh context.
	RouteMatchUnknown RouteMatchType = iota
	// RouteMatchNotFound is state when router did not find matching route for current request
	RouteMatchNotFound
	// RouteMatchMethodNotAllowed is state when router did not find route with matching path + method for current request.
	// Although router had matching route with that path but different method.
	RouteMatchMethodNotAllowed
	// RouteMatchFound is state when router found exact match for path + method combination
	RouteMatchFound
)

// RouteMatch is result object for Router.Match. Its main purpose is to avoid allocating memory for PathParams inside router.
type RouteMatch struct {
	// Type contains result as enumeration of Router.Match and helps to understand did Router actually matched Route or
	// what kind of error case (404/405) we have at the end of the handler chain.
	Type RouteMatchType
	// RoutePath contains original path with what matched route was registered with (including placeholders etc).
	RoutePath string
	// Handler is function(chain) that was matched by router. In case of no match could result to ErrNotFound or ErrMethodNotAllowed.
	Handler HandlerFunc
	// RouteInfo is information about route we just matched
	RouteInfo RouteInfo
}

// PathParams is collections of PathParam instances with various helper methods
type PathParams []PathParam

// PathParam is tuple pf path parameter name and its value in request path
type PathParam struct {
	Name  string
	Value string
}

// DefaultRouter is the registry of all registered routes for an `Echo` instance for
// request matching and URL path parameter parsing.
// Note: DefaultRouter is not coroutine-safe. Do not Add/Remove routes after HTTP server has been started with Echo.
type DefaultRouter struct {
	tree   *node
	routes Routes
	echo   *Echo

	allowOverwritingRoute    bool
	unescapePathParamValues  bool
	useEscapedPathForRouting bool
}

type children []*node

type node struct {
	kind           kind
	label          byte
	prefix         string
	parent         *node
	staticChildren children
	originalPath   string
	methods        *routeMethods
	paramChild     *node
	anyChild       *node
	paramsCount    int
	// isLeaf indicates that node does not have child routes
	isLeaf bool
	// isHandler indicates that node has at least one handler registered to it
	isHandler bool
}

type kind uint8

const (
	staticKind kind = iota
	paramKind
	anyKind

	paramLabel = byte(':')
	anyLabel   = byte('*')
)

type routeMethod struct {
	*routeInfo
	handler      HandlerFunc
	orgRouteInfo RouteInfo
}

type routeMethods struct {
	connect  *routeMethod
	delete   *routeMethod
	get      *routeMethod
	head     *routeMethod
	options  *routeMethod
	patch    *routeMethod
	post     *routeMethod
	propfind *routeMethod
	put      *routeMethod
	trace    *routeMethod
	report   *routeMethod
	anyOther map[string]*routeMethod
}

func (m *routeMethods) set(method string, r *routeMethod) {
	switch method {
	case http.MethodConnect:
		m.connect = r
	case http.MethodDelete:
		m.delete = r
	case http.MethodGet:
		m.get = r
	case http.MethodHead:
		m.head = r
	case http.MethodOptions:
		m.options = r
	case http.MethodPatch:
		m.patch = r
	case http.MethodPost:
		m.post = r
	case PROPFIND:
		m.propfind = r
	case http.MethodPut:
		m.put = r
	case http.MethodTrace:
		m.trace = r
	case REPORT:
		m.report = r
	default:
		if m.anyOther == nil {
			m.anyOther = make(map[string]*routeMethod)
		}
		if r.handler == nil {
			delete(m.anyOther, method)
		} else {
			m.anyOther[method] = r
		}
	}
}

func (m *routeMethods) find(method string) *routeMethod {
	switch method {
	case http.MethodConnect:
		return m.connect
	case http.MethodDelete:
		return m.delete
	case http.MethodGet:
		return m.get
	case http.MethodHead:
		return m.head
	case http.MethodOptions:
		return m.options
	case http.MethodPatch:
		return m.patch
	case http.MethodPost:
		return m.post
	case PROPFIND:
		return m.propfind
	case http.MethodPut:
		return m.put
	case http.MethodTrace:
		return m.trace
	case REPORT:
		return m.report
	default:
		return m.anyOther[method]
	}
}

func (m *routeMethods) isHandler() bool {
	return m.get != nil ||
		m.post != nil ||
		m.options != nil ||
		m.put != nil ||
		m.delete != nil ||
		m.connect != nil ||
		m.head != nil ||
		m.patch != nil ||
		m.propfind != nil ||
		m.trace != nil ||
		m.report != nil ||
		len(m.anyOther) != 0
}

// RouterConfig is configuration options for (default) router
type RouterConfig struct {
	// AllowOverwritingRoute instructs Router NOT to return error when new route is registered with the same method+path
	// and replaces matching route with the new one.
	AllowOverwritingRoute bool
	// UnescapePathParamValues instructs Router to unescape path parameter value when request if matched to the routes
	UnescapePathParamValues bool
	// UseEscapedPathForMatching instructs Router to use escaped request URL path (req.URL.Path) for matching the request.
	UseEscapedPathForMatching bool
}

// NewRouter returns a new Router instance.
func NewRouter(e *Echo, config RouterConfig) *DefaultRouter {
	r := &DefaultRouter{
		tree: &node{
			methods:   new(routeMethods),
			isLeaf:    true,
			isHandler: false,
		},
		routes: make(Routes, 0),
		echo:   e,

		allowOverwritingRoute:    config.AllowOverwritingRoute,
		unescapePathParamValues:  config.UnescapePathParamValues,
		useEscapedPathForRouting: config.UseEscapedPathForMatching,
	}
	return r
}

// Routes returns all registered routes
func (r *DefaultRouter) Routes() Routes {
	return r.routes
}

// Remove unregisters registered route
func (r *DefaultRouter) Remove(method string, path string) error {
	currentNode := r.tree
	if currentNode == nil || (currentNode.isLeaf && !currentNode.isHandler) {
		return errors.New("router has no routes to remove")
	}

	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		path = "/" + path
	}

	var nodeToRemove *node
	prefixLen := 0
	for {
		if currentNode.originalPath == path && currentNode.isHandler {
			nodeToRemove = currentNode
			break
		}
		if currentNode.kind == staticKind {
			prefixLen = prefixLen + len(currentNode.prefix)
		} else {
			prefixLen = len(currentNode.originalPath)
		}

		if prefixLen >= len(path) {
			break
		}

		next := path[prefixLen]
		switch next {
		case paramLabel:
			currentNode = currentNode.paramChild
		case anyLabel:
			currentNode = currentNode.anyChild
		default:
			currentNode = currentNode.findStaticChild(next)
		}

		if currentNode == nil {
			break
		}
	}

	if nodeToRemove == nil {
		return errors.New("could not find route to remove by given path")
	}

	if !nodeToRemove.isHandler {
		return errors.New("could not find route to remove by given path")
	}

	if mh := nodeToRemove.methods.find(method); mh == nil {
		return errors.New("could not find route to remove by given path and method")
	}
	nodeToRemove.setHandler(method, nil)

	var rIndex int
	for i, rr := range r.routes {
		if rr.Method() == method && rr.Path() == path {
			rIndex = i
			break
		}
	}
	r.routes = append(r.routes[:rIndex], r.routes[rIndex+1:]...)

	if !nodeToRemove.isHandler && nodeToRemove.isLeaf {
		// TODO: if !nodeToRemove.isLeaf and has at least 2 children merge paths for remaining nodes?
		current := nodeToRemove
		for {
			parent := current.parent
			if parent == nil {
				break
			}
			switch current.kind {
			case staticKind:
				var index int
				for i, c := range parent.staticChildren {
					if c == current {
						index = i
						break
					}
				}
				parent.staticChildren = append(parent.staticChildren[:index], parent.staticChildren[index+1:]...)
			case paramKind:
				parent.paramChild = nil
			case anyKind:
				parent.anyChild = nil
			}

			parent.isLeaf = parent.anyChild == nil && parent.paramChild == nil && len(parent.staticChildren) == 0
			if !parent.isLeaf || parent.isHandler {
				break
			}
			current = parent
		}
	}

	return nil
}

// AddRouteError is error returned by Router.Add containing information what actual route adding failed. Useful for
// mass adding (i.e. Any() routes)
type AddRouteError struct {
	Method string
	Path   string
	Err    error
}

func (e *AddRouteError) Error() string { return e.Method + " " + e.Path + ": " + e.Err.Error() }

func (e *AddRouteError) Unwrap() error { return e.Err }

func newAddRouteError(route Route, err error) *AddRouteError {
	return &AddRouteError{
		Method: route.Method,
		Path:   route.Path,
		Err:    err,
	}
}

// Add registers a new route for method and path with matching handler.
func (r *DefaultRouter) Add(routable Routable) (RouteInfo, error) {
	route := routable.ToRoute()
	if route.Handler == nil {
		return nil, newAddRouteError(route, errors.New("adding route without handler function"))
	}
	method := route.Method
	path := route.Path
	h := applyMiddleware(route.Handler, route.Middlewares...)
	if !r.allowOverwritingRoute {
		for _, rr := range r.routes {
			if route.Method == rr.Method() && route.Path == rr.Path() {
				return nil, newAddRouteError(route, errors.New("adding duplicate route (same method+path) is not allowed"))
			}
		}
	}

	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		path = "/" + path
	}
	paramNames := make([]string, 0)
	originalPath := path
	wasAdded := false
	var ri RouteInfo
	for i, lcpIndex := 0, len(path); i < lcpIndex; i++ {
		if path[i] == paramLabel {
			if i > 0 && path[i-1] == '\\' {
				continue
			}
			j := i + 1

			r.insert(staticKind, path[:i], method, routeMethod{routeInfo: &routeInfo{method: method}})
			for ; i < lcpIndex && path[i] != '/'; i++ {
			}

			paramNames = append(paramNames, path[j:i])
			path = path[:j] + path[i:]
			i, lcpIndex = j, len(path)

			if i == lcpIndex {
				// path node is last fragment of route path. ie. `/users/:id`
				ri = routable.ToRouteInfo(paramNames)
				rm := routeMethod{
					routeInfo:    &routeInfo{method: method, path: originalPath, params: paramNames, name: route.Name},
					handler:      h,
					orgRouteInfo: ri,
				}
				r.insert(paramKind, path[:i], method, rm)
				wasAdded = true
				break
			} else {
				r.insert(paramKind, path[:i], method, routeMethod{routeInfo: &routeInfo{method: method}})
			}
		} else if path[i] == anyLabel {
			r.insert(staticKind, path[:i], method, routeMethod{routeInfo: &routeInfo{method: method}})
			paramNames = append(paramNames, "*")
			ri = routable.ToRouteInfo(paramNames)
			rm := routeMethod{
				routeInfo:    &routeInfo{method: method, path: originalPath, params: paramNames, name: route.Name},
				handler:      h,
				orgRouteInfo: ri,
			}
			r.insert(anyKind, path[:i+1], method, rm)
			wasAdded = true
			break
		}
	}

	if !wasAdded {
		ri = routable.ToRouteInfo(paramNames)
		rm := routeMethod{
			routeInfo:    &routeInfo{method: method, path: originalPath, params: paramNames, name: route.Name},
			handler:      h,
			orgRouteInfo: ri,
		}
		r.insert(staticKind, path, method, rm)
	}

	r.storeRouteInfo(ri)

	return ri, nil
}

func (r *DefaultRouter) storeRouteInfo(ri RouteInfo) {
	for i, rr := range r.routes {
		if ri.Method() == rr.Method() && ri.Path() == rr.Path() {
			r.routes[i] = ri
			return
		}
	}
	r.routes = append(r.routes, ri)
}

func (r *DefaultRouter) insert(t kind, path string, method string, ri routeMethod) {
	currentNode := r.tree // Current node as root
	search := path

	for {
		searchLen := len(search)
		prefixLen := len(currentNode.prefix)
		lcpLen := 0

		// LCP - Longest Common Prefix (https://en.wikipedia.org/wiki/LCP_array)
		max := prefixLen
		if searchLen < max {
			max = searchLen
		}
		for ; lcpLen < max && search[lcpLen] == currentNode.prefix[lcpLen]; lcpLen++ {
		}

		if lcpLen == 0 {
			// At root node
			currentNode.label = search[0]
			currentNode.prefix = search
			if ri.handler != nil {
				currentNode.kind = t
				currentNode.setHandler(method, &ri)
				currentNode.paramsCount = len(ri.params)
				currentNode.originalPath = ri.path
			}
			currentNode.isLeaf = currentNode.staticChildren == nil && currentNode.paramChild == nil && currentNode.anyChild == nil
		} else if lcpLen < prefixLen {
			// Split node
			n := newNode(
				currentNode.kind,
				currentNode.prefix[lcpLen:],
				currentNode,
				currentNode.staticChildren,
				currentNode.methods,
				currentNode.paramsCount,
				currentNode.originalPath,
				currentNode.paramChild,
				currentNode.anyChild,
			)
			// Update parent path for all children to new node
			for _, child := range currentNode.staticChildren {
				child.parent = n
			}
			if currentNode.paramChild != nil {
				currentNode.paramChild.parent = n
			}
			if currentNode.anyChild != nil {
				currentNode.anyChild.parent = n
			}

			// Reset parent node
			currentNode.kind = staticKind
			currentNode.label = currentNode.prefix[0]
			currentNode.prefix = currentNode.prefix[:lcpLen]
			currentNode.staticChildren = nil
			currentNode.methods = new(routeMethods)
			currentNode.originalPath = ""
			currentNode.paramsCount = 0
			currentNode.paramChild = nil
			currentNode.anyChild = nil
			currentNode.isLeaf = false
			currentNode.isHandler = false

			// Only Static children could reach here
			currentNode.addStaticChild(n)

			if lcpLen == searchLen {
				// At parent node
				currentNode.kind = t
				if ri.handler != nil {
					currentNode.setHandler(method, &ri)
					currentNode.paramsCount = len(ri.params)
					currentNode.originalPath = ri.path
				}
			} else {
				// Create child node
				n = newNode(t, search[lcpLen:], currentNode, nil, new(routeMethods), 0, ri.path, nil, nil)
				if ri.handler != nil {
					n.setHandler(method, &ri)
					n.paramsCount = len(ri.params)
				}
				// Only Static children could reach here
				currentNode.addStaticChild(n)
			}
			currentNode.isLeaf = currentNode.staticChildren == nil && currentNode.paramChild == nil && currentNode.anyChild == nil
		} else if lcpLen < searchLen {
			search = search[lcpLen:]
			c := currentNode.findChildWithLabel(search[0])
			if c != nil {
				// Go deeper
				currentNode = c
				continue
			}
			// Create child node
			n := newNode(t, search, currentNode, nil, new(routeMethods), 0, ri.path, nil, nil)
			if ri.handler != nil {
				n.setHandler(method, &ri)
				n.paramsCount = len(ri.params)
			}
			switch t {
			case staticKind:
				currentNode.addStaticChild(n)
			case paramKind:
				currentNode.paramChild = n
			case anyKind:
				currentNode.anyChild = n
			}
			currentNode.isLeaf = currentNode.staticChildren == nil && currentNode.paramChild == nil && currentNode.anyChild == nil
		} else {
			// Node already exists
			if ri.handler != nil {
				currentNode.setHandler(method, &ri)
				currentNode.paramsCount = len(ri.params)
				currentNode.originalPath = ri.path
			}
		}
		return
	}
}

func newNode(t kind, pre string, p *node, sc children, mh *routeMethods, paramsCount int, ppath string, paramChildren, anyChildren *node) *node {
	return &node{
		kind:           t,
		label:          pre[0],
		prefix:         pre,
		parent:         p,
		staticChildren: sc,
		originalPath:   ppath,
		paramsCount:    paramsCount,
		methods:        mh,
		paramChild:     paramChildren,
		anyChild:       anyChildren,
		isLeaf:         sc == nil && paramChildren == nil && anyChildren == nil,
		isHandler:      mh.isHandler(),
	}
}

func (n *node) addStaticChild(c *node) {
	n.staticChildren = append(n.staticChildren, c)
}

func (n *node) findStaticChild(l byte) *node {
	for _, c := range n.staticChildren {
		if c.label == l {
			return c
		}
	}
	return nil
}

func (n *node) findChildWithLabel(l byte) *node {
	for _, c := range n.staticChildren {
		if c.label == l {
			return c
		}
	}
	if l == paramLabel {
		return n.paramChild
	}
	if l == anyLabel {
		return n.anyChild
	}
	return nil
}

func (n *node) setHandler(method string, r *routeMethod) {
	n.methods.set(method, r)
	if r != nil && r.handler != nil {
		n.isHandler = true
	} else {
		n.isHandler = n.methods.isHandler()
	}
}

const (
	// NotFoundRouteName is name of RouteInfo returned when router did not find matching route (404: not found).
	NotFoundRouteName = "EchoRouteNotFound"
	// MethodNotAllowedRouteName is name of RouteInfo returned when router did not find matching method for route  (404: method not allowed).
	MethodNotAllowedRouteName = "EchoRouteMethodNotAllowed"
)

// Note: notFoundRouteInfo exists to avoid allocations when setting 404 RouteInfo to RouteMatch
var notFoundRouteInfo = &routeInfo{
	method: "",
	path:   "",
	params: nil,
	name:   NotFoundRouteName,
}

// Note: methodNotAllowedRouteInfo exists to avoid allocations when setting 405 RouteInfo to RouteMatch
var methodNotAllowedRouteInfo = &routeInfo{
	method: "",
	path:   "",
	params: nil,
	name:   MethodNotAllowedRouteName,
}

// notFoundHandler is handler for 404 cases
// Handle returned ErrNotFound errors in Echo.HTTPErrorHandler
var notFoundHandler = func(c Context) error {
	return ErrNotFound
}

// methodNotAllowedHandler is handler for case when route for path+method match was not found (http code 405)
// Handle returned ErrMethodNotAllowed errors in Echo.HTTPErrorHandler
var methodNotAllowedHandler = func(c Context) error {
	return ErrMethodNotAllowed
}

// Match looks up a handler registered for method and path. It also parses URL for path parameters and loads them
// into context.
//
// For performance:
//
// - Get context from `Echo#AcquireContext()`
// - Reset it `Context#Reset()`
// - Return it `Echo#ReleaseContext()`.
func (r *DefaultRouter) Match(req *http.Request, pathParams *PathParams) RouteMatch {
	*pathParams = (*pathParams)[0:cap(*pathParams)]

	path := req.URL.Path
	if !r.useEscapedPathForRouting && req.URL.RawPath != "" {
		// Difference between URL.RawPath and URL.Path is:
		//  * URL.Path is where request path is stored. Value is stored in decoded form: /%47%6f%2f becomes /Go/.
		//  * URL.RawPath is an optional field which only gets set if the default encoding is different from Path.
		path = req.URL.RawPath
	}
	var (
		currentNode           = r.tree // root as current node
		previousBestMatchNode *node
		matchedRouteMethod    *routeMethod
		// search stores the remaining path to check for match. By each iteration we move from start of path to end of the path
		// and search value gets shorter and shorter.
		search      = path
		searchIndex = 0
		paramIndex  int // Param counter
	)

	// Backtracking is needed when a dead end (leaf node) is reached in the router tree.
	// To backtrack the current node will be changed to the parent node and the next kind for the
	// router logic will be returned based on fromKind or kind of the dead end node (static > param > any).
	// For example if there is no static node match we should check parent next sibling by kind (param).
	// Backtracking itself does not check if there is a next sibling, this is done by the router logic.
	backtrackToNextNodeKind := func(fromKind kind) (nextNodeKind kind, valid bool) {
		previous := currentNode
		currentNode = previous.parent
		valid = currentNode != nil

		// Next node type by priority
		if previous.kind == anyKind {
			nextNodeKind = staticKind
		} else {
			nextNodeKind = previous.kind + 1
		}

		if fromKind == staticKind {
			// when backtracking is done from static kind block we did not change search so nothing to restore
			return
		}

		// restore search to value it was before we move to current node we are backtracking from.
		if previous.kind == staticKind {
			searchIndex -= len(previous.prefix)
		} else {
			paramIndex--
			// for param/any node.prefix value is always `:` so we can not deduce searchIndex from that and must use pValue
			// for that index as it would also contain part of path we cut off before moving into node we are backtracking from
			searchIndex -= len((*pathParams)[paramIndex].Value)
			(*pathParams)[paramIndex].Value = ""
		}
		search = path[searchIndex:]
		return
	}

	// Router tree is implemented by longest common prefix array (LCP array) https://en.wikipedia.org/wiki/LCP_array
	// Tree search is implemented as for loop where one loop iteration is divided into 3 separate blocks
	// Each of these blocks checks specific kind of node (static/param/any). Order of blocks reflex their priority in routing.
	// Search order/priority is: static > param > any.
	//
	// Note: backtracking in tree is implemented by replacing/switching currentNode to previous node
	// and hoping to (goto statement) next block by priority to check if it is the match.
	for {
		prefixLen := 0 // Prefix length
		lcpLen := 0    // LCP (longest common prefix) length

		if currentNode.kind == staticKind {
			searchLen := len(search)
			prefixLen = len(currentNode.prefix)

			// LCP - Longest Common Prefix (https://en.wikipedia.org/wiki/LCP_array)
			max := prefixLen
			if searchLen < max {
				max = searchLen
			}
			for ; lcpLen < max && search[lcpLen] == currentNode.prefix[lcpLen]; lcpLen++ {
			}
		}

		if lcpLen != prefixLen {
			// No matching prefix, let's backtrack to the first possible alternative node of the decision path
			nk, ok := backtrackToNextNodeKind(staticKind)
			if !ok {
				break // No other possibilities on the decision path
			} else if nk == paramKind {
				goto Param
				// NOTE: this case (backtracking from static node to previous any node) can not happen by current any matching logic. Any node is end of search currently
				//} else if nk == anyKind {
				//	goto Any
			} else {
				// Not found (this should never be possible for static node we are looking currently)
				break
			}
		}

		// The full prefix has matched, remove the prefix from the remaining search
		search = search[lcpLen:]
		searchIndex = searchIndex + lcpLen

		// Finish routing if no remaining search and we are on a node with handler and matching method type
		if search == "" && currentNode.isHandler {
			// check if current node has handler registered for http method we are looking for. we store currentNode as
			// best matching in case we do no find no more routes matching this path+method
			if previousBestMatchNode == nil {
				previousBestMatchNode = currentNode
			}
			if rMethod := currentNode.methods.find(req.Method); rMethod != nil {
				matchedRouteMethod = rMethod
				break
			}
		}

		// Static node
		if search != "" {
			if child := currentNode.findStaticChild(search[0]); child != nil {
				currentNode = child
				continue
			}
		}

	Param:
		// Param node
		if child := currentNode.paramChild; search != "" && child != nil {
			currentNode = child
			i := 0
			l := len(search)
			if currentNode.isLeaf {
				// when param node does not have any children then param node should act similarly to any node - consider all remaining search as match
				i = l
			} else {
				for ; i < l && search[i] != '/'; i++ {
				}
			}

			(*pathParams)[paramIndex].Value = search[:i]
			paramIndex++
			search = search[i:]
			searchIndex = searchIndex + i
			continue
		}

	Any:
		// Any node
		if child := currentNode.anyChild; child != nil {
			// If any node is found, use remaining path for paramValues
			currentNode = child
			(*pathParams)[currentNode.paramsCount-1].Value = search
			// update indexes/search in case we need to backtrack when no handler match is found
			paramIndex++
			searchIndex += +len(search)
			search = ""

			// check if current node has handler registered for http method we are looking for. we store currentNode as
			// best matching in case we do no find no more routes matching this path+method
			if previousBestMatchNode == nil {
				previousBestMatchNode = currentNode
			}
			if rMethod := currentNode.methods.find(req.Method); rMethod != nil {
				matchedRouteMethod = rMethod
				break
			}
		}

		// Let's backtrack to the first possible alternative node of the decision path
		nk, ok := backtrackToNextNodeKind(anyKind)
		if !ok {
			break // No other possibilities on the decision path
		} else if nk == paramKind {
			goto Param
		} else if nk == anyKind {
			goto Any
		} else {
			// Not found
			break
		}
	}

	result := RouteMatch{
		Type:      RouteMatchNotFound,
		Handler:   notFoundHandler,
		RoutePath: "",
		RouteInfo: notFoundRouteInfo,
	}
	if currentNode == nil && previousBestMatchNode == nil {
		*pathParams = (*pathParams)[0:0]
		return result // nothing matched at all with given path
	}

	if matchedRouteMethod != nil {
		result.Type = RouteMatchFound
		result.Handler = matchedRouteMethod.handler
		result.RoutePath = matchedRouteMethod.routeInfo.path
		result.RouteInfo = matchedRouteMethod.routeInfo
	} else {
		// use previous match as basis. although we have no matching handler we have path match.
		// so we can send http.StatusMethodNotAllowed (405) instead of http.StatusNotFound (404)
		currentNode = previousBestMatchNode

		// this here is only reason why `RouteMatch.RoutePath` exists. We do not want to create new RouteInfo just for path.
		result.RoutePath = currentNode.originalPath
		if currentNode.isHandler {
			// TODO: in case of OPTIONS method we could respond with list of methods that node has. See https://httpwg.org/specs/rfc7231.html#OPTIONS
			result.Type = RouteMatchMethodNotAllowed
			result.Handler = methodNotAllowedHandler
			result.RouteInfo = methodNotAllowedRouteInfo
		}
	}

	*pathParams = (*pathParams)[0:currentNode.paramsCount]
	if matchedRouteMethod != nil {
		for i, name := range matchedRouteMethod.params {
			(*pathParams)[i].Name = name
		}
	}

	if r.unescapePathParamValues && currentNode.kind != staticKind {
		// See issue #1531, #1258 - there are cases when path parameter need to be unescaped
		for i, p := range *pathParams {
			tmpVal, err := url.PathUnescape(p.Value)
			if err == nil { // handle problems by ignoring them.
				(*pathParams)[i].Value = tmpVal
			}
		}
	}

	return result
}

// Get returns path parameter value for given name or default value.
func (p PathParams) Get(name string, defaultValue string) string {
	for _, param := range p {
		if param.Name == name {
			return param.Value
		}
	}
	return defaultValue
}
