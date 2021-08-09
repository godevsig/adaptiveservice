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
