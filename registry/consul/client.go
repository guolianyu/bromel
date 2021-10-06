package consul

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/phuhao00/bromel/logger"
	"go.uber.org/zap"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/phuhao00/bromel/registry"
)

// Client is consul client config
type Client struct {
	cli    *api.Client
	ctx    context.Context
	cancel context.CancelFunc
	logger *logger.Logger
}

// NewClientX creates consul client
func NewClientX(cli *api.Client) *Client {
	c := &Client{cli: cli}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c
}

// Service get services from consul
func (d *Client) Service(ctx context.Context, service string, index uint64, passingOnly bool) ([]*registry.ServiceInstance, uint64, error) {
	opts := &api.QueryOptions{
		WaitIndex: index,
		WaitTime:  time.Second * 55,
	}
	opts = opts.WithContext(ctx)
	entries, meta, err := d.cli.Health().Service(service, "", passingOnly, opts)
	if err != nil {
		return nil, 0, err
	}
	var services []*registry.ServiceInstance
	for _, entry := range entries {
		var version string
		for _, tag := range entry.Service.Tags {
			strs := strings.SplitN(tag, "=", 2)
			if len(strs) == 2 && strs[0] == "version" {
				version = strs[1]
			}
		}
		var endpoints []string
		for scheme, addr := range entry.Service.TaggedAddresses {
			if scheme == "lan_ipv4" || scheme == "wan_ipv4" || scheme == "lan_ipv6" || scheme == "wan_ipv6" {
				continue
			}
			endpoints = append(endpoints, addr.Address)
		}
		services = append(services, &registry.ServiceInstance{
			ID:        entry.Service.ID,
			Name:      entry.Service.Service,
			Metadata:  entry.Service.Meta,
			Version:   version,
			Endpoints: endpoints,
		})
	}
	return services, meta.LastIndex, nil
}

// Register register service instacen to consul
func (d *Client) Register(ctx context.Context, svc *registry.ServiceInstance, enableHealthCheck bool) error {
	addresses := make(map[string]api.ServiceAddress)
	var addr string
	var port uint64
	for _, endpoint := range svc.Endpoints {
		raw, err := url.Parse(endpoint)
		if err != nil {
			return err
		}
		addr = raw.Hostname()
		port, _ = strconv.ParseUint(raw.Port(), 10, 16)
		addresses[raw.Scheme] = api.ServiceAddress{Address: endpoint, Port: int(port)}
	}
	asr := &api.AgentServiceRegistration{
		ID:              svc.ID,
		Name:            svc.Name,
		Meta:            svc.Metadata,
		Tags:            []string{fmt.Sprintf("version=%s", svc.Version)},
		TaggedAddresses: addresses,
		Address:         addr,
		Port:            int(port),
		Checks: []*api.AgentServiceCheck{
			{
				CheckID: "service:" + svc.ID,
				TTL:     "50s",
				Status:  "passing",
			},
		},
	}
	if enableHealthCheck {
		asr.Checks = append(asr.Checks, &api.AgentServiceCheck{
			TCP:      fmt.Sprintf("%s:%d", addr, port),
			Interval: "20s",
			Status:   "passing",
		})
	}

	ch := make(chan error, 1)
	go func() {
		err := d.cli.Agent().ServiceRegister(asr)
		ch <- err
	}()
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
		fmt.Println(err)
	case err = <-ch:
		fmt.Println(err)
	}
	if err == nil {
		go func() {
			ticker := time.NewTicker(time.Second * 20)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					err = d.cli.Agent().UpdateTTL("service:"+svc.ID, "pass", "pass")
					fmt.Println(err)
				case <-d.ctx.Done():
					fmt.Println("llllllllllllllllll")
					return
				}
			}
		}()
	}
	return err
}

//Deregister deregister service by service ID
func (d *Client) Deregister(ctx context.Context, serviceID string) error {
	d.cancel()
	ch := make(chan error, 1)
	go func() {
		err := d.cli.Agent().ServiceDeregister(serviceID)
		ch <- err
	}()
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-ch:
	}
	return err
}

//LoadJSONFromConsulKV ...
func (c *Client) LoadJSONFromConsulKV(configKey string, cfg interface{}) {
	kv := c.cli.KV()
	kvPair, _, err := kv.Get(configKey, nil)
	if err != nil {
		c.logger.Error("[LoadJSONFromConsulKV]", zap.String("err", err.Error()))
		return
	}
	if kvPair == nil {
		c.logger.Error("[LoadJSONFromConsulKV]", zap.String("kvPair", "kvPair is nil"))
		return
	}
	if len(kvPair.Value) == 0 {
		c.logger.Error("[LoadJSONFromConsulKV]", zap.String("kvPair.Value", "kvPair.Value length is 0"))
		return
	}
	if err = json.Unmarshal(kvPair.Value, &cfg); err != nil {
		c.logger.Error("[LoadJSONFromConsulKV]", zap.String("Unmarshal kvPair.Value", err.Error()))
	}
}

//PutKeyValueToConsul ...
func (c *Client) PutKeyValueToConsul(key string, value []byte) {
	kv := c.cli.KV()
	pair := &api.KVPair{Key: key, Value: value}
	if _, err := kv.Put(pair, nil); err != nil {
		c.logger.Error("[PutKeyValueToConsul]", zap.String(" kv.Put", err.Error()))
	}
}
