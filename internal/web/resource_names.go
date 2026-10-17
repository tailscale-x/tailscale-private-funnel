package web

func (c *Control) nodeDisplayName(id string) string {
	n, err := c.Store.Node(id)
	if err != nil {
		return id
	}

	return n.Name + " · " + n.Hostname + " · " + id[:min(len(id), 6)]
}
