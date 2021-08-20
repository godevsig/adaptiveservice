package adaptiveservice

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
