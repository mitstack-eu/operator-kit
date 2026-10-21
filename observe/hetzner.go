package observe

import (
	"context"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

type HetznerServerDescription struct {
	ID       string
	Status   string
	PublicIP string
	Location string
}

type HetznerServerAPI interface {
	GetServer(ctx context.Context, d contract.Desired, serverID string) (desc HetznerServerDescription, found bool, err error)
}

// HetznerServer observes a hetzner.server by its serverId: running is
// present, missing absent, any other status unknown.
type HetznerServer struct {
	API HetznerServerAPI
}

func (o HetznerServer) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "serverId")
	if !ok {
		return obs, nil
	}
	desc, found, err := o.API.GetServer(ctx, r.Desired, ids[0])
	switch {
	case err != nil || !found:
		return existence(found, err, nil)
	case desc.Status != "running":
		return unknown("resource %q: server %s is %s", r.Name, ids[0], desc.Status), nil
	}
	outputs := map[string]string{"hostId": desc.ID, "execKind": "ssh"}
	if desc.Location != "" {
		outputs["cloud.region"] = desc.Location
	}
	if desc.PublicIP != "" {
		outputs["execTarget"] = desc.PublicIP
	}
	return present(outputs), nil
}

type HetznerPrimaryIPDescription struct {
	ID string
	IP string
}

type HetznerPrimaryIPAPI interface {
	GetPrimaryIP(ctx context.Context, d contract.Desired, id string) (desc HetznerPrimaryIPDescription, found bool, err error)
}

// HetznerPrimaryIP observes a hetzner.primary-ip by its primaryIpId,
// reporting its address as publicIp.
type HetznerPrimaryIP struct {
	API HetznerPrimaryIPAPI
}

func (o HetznerPrimaryIP) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "primaryIpId")
	if !ok {
		return obs, nil
	}
	desc, found, err := o.API.GetPrimaryIP(ctx, r.Desired, ids[0])
	if err != nil || !found {
		return existence(found, err, nil)
	}
	return present(map[string]string{"publicIp": desc.IP}), nil
}

type HetznerNetworkAPI interface {
	NetworkExists(ctx context.Context, d contract.Desired, networkID string) (bool, error)
}

// HetznerNetwork observes a hetzner.network by its networkId.
type HetznerNetwork struct {
	API HetznerNetworkAPI
}

func (o HetznerNetwork) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "networkId")
	if !ok {
		return obs, nil
	}
	exists, err := o.API.NetworkExists(ctx, r.Desired, ids[0])
	return existence(exists, err, nil)
}

type HetznerFirewallAPI interface {
	FirewallExists(ctx context.Context, d contract.Desired, firewallID string) (bool, error)
}

// HetznerFirewall observes a hetzner.firewall by its firewallId.
type HetznerFirewall struct {
	API HetznerFirewallAPI
}

func (o HetznerFirewall) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "firewallId")
	if !ok {
		return obs, nil
	}
	exists, err := o.API.FirewallExists(ctx, r.Desired, ids[0])
	return existence(exists, err, nil)
}
