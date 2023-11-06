package adaptiveservice

// ProviderSelectionMethod is the method to select the most suitable service provider
// when client discovered provider candidates more than one.
//
// Random the last method used when there are still more than one
// providers after other ProviderSelectionMethods were applied.
type ProviderSelectionMethod uint16

const (
	// Capacity is the strategy to select the service provider which should
	// be more capable to serve the client. The selected provider
	// has fewer items to precess or is under lighter working load
	// or has more workers.
	Capacity ProviderSelectionMethod = iota + 1
	// Latency is the strategy to select the service provider that has faster
	// response time, this usually indicates better networking connection
	// and the service provider is in a healthy responsive condition.
	Latency
	// Invalid is invalid strategy
	Invalid
)

// SetProviderSelectionMethod sets the ProviderSelectionMethod.
// It can be called multiple times with different ProviderSelectionMethod and the
// provider selection procedure will follow the calling sequence, e.g.
//  c.SetProviderSelectionMethod(Capacity).SetProviderSelectionMethod(Latency)
// In this case, it will first select the top 50% of the provider(s) with Capacity
// method. If there are still multiple providers, the Latency method is then
// applied in turn to also select top 50%. The Random ProviderSelectionMethod
// is applied at last if needed to ensure that wanted number of provider(s)
// will be ultimately selected.
func (c *Client) SetProviderSelectionMethod(method ProviderSelectionMethod) *Client {
	if method > 0 && method < Invalid {
		c.providerSelectionMethods = append(c.providerSelectionMethods, method)
	}
	return c
}

// SetDiscoverTimeout sets the wait timeout in seconds of the discover procedure.
// The discover procedure waits for the wanted service to be available until timeout.
// The connection channel returned by Discover() will be closed if timeout happens.
//  > 0 : wait timeout seconds
//  = 0 : do not wait
//  < 0 : wait forever
// Default -1.
func (c *Client) SetDiscoverTimeout(timeout int) *Client {
	c.discoverTimeout = timeout
	return c
}

// SetDeepCopy sets the client to deep copy the message before sending it out.
// This option is only useful for process scope where by default the server
// can modify the message that the client owns because server and client in
// the same process space, and this case only happens when the message handler
// in the server has reference receiver.
//
// Unexported fields in the message are not copied but set to zero value.
// Default is zero copy.
func (c *Client) SetDeepCopy() *Client {
	c.deepCopy = true
	return c
}
