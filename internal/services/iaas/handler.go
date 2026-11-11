// Package iaas mocks the IaaS API (v2) under /iaas.
package iaas

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/morks/stackit_mock/internal/crud"
	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/store"
)

const (
	pre = "/iaas"
	R   = pre + "/v2/projects/{projectId}/regions/{region}"
	O   = pre + "/v2/organizations/{organizationId}"
)

func projScope(r *http.Request) crud.Scope {
	return crud.Scope{Project: r.PathValue("projectId"), Region: r.PathValue("region")}
}

var netCounter atomic.Int32

type h struct {
	d                                                                                            *httpx.Deps
	networks, nics, nicsGet, volumes, servers, ips, sgs, rules, keypairs, images, areas, regions *crud.Coll
	requests                                                                                     sync.Map // requestId -> map[string]any
}

func Register(mux *http.ServeMux, d *httpx.Deps) {
	x := &h{d: d}
	x.define()

	x.networks.Routes(mux, R+"/networks")
	x.sgs.Routes(mux, R+"/security-groups")
	x.volumes.Routes(mux, R+"/volumes")
	x.servers.Routes(mux, R+"/servers")
	x.ips.Routes(mux, R+"/public-ips")
	mux.HandleFunc("POST "+R+"/images", x.images.Create)
	mux.HandleFunc("GET "+R+"/images", x.images.List)
	mux.HandleFunc("GET "+R+"/images/{imageId}", x.getImage)
	mux.HandleFunc("PATCH "+R+"/images/{imageId}", x.images.Patch)
	mux.HandleFunc("DELETE "+R+"/images/{imageId}", x.images.Delete)
	x.keypairs.Routes(mux, pre+"/v2/keypairs")
	x.areas.Routes(mux, O+"/network-areas")

	// rules (nested)
	rb := R + "/security-groups/{sgId}/rules"
	mux.HandleFunc("POST "+rb, x.rules.Create)
	mux.HandleFunc("GET "+rb, x.rules.List)
	mux.HandleFunc("GET "+rb+"/{ruleId}", x.rules.Get)
	mux.HandleFunc("DELETE "+rb+"/{ruleId}", x.rules.Delete)

	// NICs
	nb := R + "/networks/{networkId}/nics"
	mux.HandleFunc("POST "+nb, x.nics.Create)
	mux.HandleFunc("GET "+nb, x.nics.List)
	mux.HandleFunc("GET "+nb+"/{nicId}", x.nicsGet.Get)
	mux.HandleFunc("PATCH "+nb+"/{nicId}", x.nicsGet.Patch)
	mux.HandleFunc("DELETE "+nb+"/{nicId}", x.nicsGet.Delete)
	mux.HandleFunc("GET "+R+"/nics", x.nicsGet.List)
	mux.HandleFunc("GET "+R+"/nics/{nicId}", x.nicsGet.Get)

	// network area regions
	ab := O + "/network-areas/{areaId}"
	mux.HandleFunc("GET "+ab+"/projects", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]any{"items": []any{}})
	})
	mux.HandleFunc("GET "+ab+"/regions", x.listAreaRegions)
	mux.HandleFunc("PUT "+ab+"/regions/{region}", x.regions.Upsert)
	mux.HandleFunc("GET "+ab+"/regions/{region}", x.regions.Get)
	mux.HandleFunc("PATCH "+ab+"/regions/{region}", x.regions.Patch)
	mux.HandleFunc("DELETE "+ab+"/regions/{region}", x.regions.Delete)

	// server actions and attachments
	for action, status := range map[string]string{"start": "ACTIVE", "stop": "INACTIVE", "deallocate": "DEALLOCATED",
		"reboot": "ACTIVE", "rescue": "RESCUE", "unrescue": "ACTIVE"} {
		mux.HandleFunc("POST "+R+"/servers/{serverId}/"+action, x.serverAction(status))
	}
	mux.HandleFunc("POST "+R+"/servers/{serverId}/resize", x.serverResize)
	for _, kind := range []string{"nics", "public-ips", "volume-attachments", "security-groups", "service-accounts"} {
		mux.HandleFunc("PUT "+R+"/servers/{serverId}/"+kind+"/{attId}", x.attach(kind, true))
		mux.HandleFunc("DELETE "+R+"/servers/{serverId}/"+kind+"/{attId}", x.attach(kind, false))
	}
	mux.HandleFunc("GET "+R+"/servers/{serverId}/volume-attachments/{attId}", x.getVolumeAttachment)
	mux.HandleFunc("POST "+R+"/volumes/{volumeId}/resize", x.volumeResize)
	mux.HandleFunc("GET "+R+"/requests/{requestId}", x.getRequest)

	// static data
	mux.HandleFunc("GET "+R+"/machine-types", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]any{"items": machineTypes})
	})
	mux.HandleFunc("GET "+R+"/machine-types/{name}", func(w http.ResponseWriter, r *http.Request) {
		for _, m := range machineTypes {
			if m["name"] == r.PathValue("name") {
				httpx.WriteJSON(w, 200, m)
				return
			}
		}
		httpx.ErrIaaS(w, r, 404, "machine type not found")
	})
	mux.HandleFunc("GET "+pre+"/v2/regions/{region}/availability-zones", func(w http.ResponseWriter, r *http.Request) {
		reg := r.PathValue("region")
		httpx.WriteJSON(w, 200, map[string]any{"items": []string{reg + "-1", reg + "-2", reg + "-3"}})
	})
	mux.HandleFunc("PUT /upload/images/{imageId}", x.uploadImage)
}

var errBad = errors.New

var protoNumbers = map[string]int{"icmp": 1, "tcp": 6, "udp": 17, "ipv6-icmp": 58}

func (x *h) exists(typ, project, id string) bool {
	_, ok := x.d.Store.Get(typ, project, id)
	return ok
}

func (x *h) define() {
	d := x.d
	base := func(typ, label, idParam string) *crud.Coll {
		return &crud.Coll{D: d, Err: httpx.ErrIaaS, Type: typ, Service: "iaas", Label: label,
			IDParam: idParam, IDKey: "id", Stamp: true, Scope: projScope}
	}

	x.networks = base("iaas/network", "network", "networkId")
	x.networks.Unique = true
	x.networks.StatusKey, x.networks.Initial, x.networks.Final, x.networks.Updating, x.networks.Deleting =
		"status", "CREATING", "CREATED", "UPDATING", "DELETING"
	x.networks.Prepare = func(r *http.Request, s crud.Scope, b map[string]any) error {
		if httpx.Str(b, "name") == "" {
			return errBad("name is required")
		}
		if _, ok := b["dhcp"]; !ok {
			b["dhcp"] = true
		}
		if _, ok := b["routed"]; !ok {
			b["routed"] = false
		}
		if v4 := crud.Map(b["ipv4"]); v4 != nil {
			if p := httpx.Str(v4, "prefix"); p != "" {
				v4["prefixes"] = []any{p}
			} else if _, ok := v4["prefixLength"]; ok {
				n := netCounter.Add(1)
				v4["prefixes"] = []any{fmt.Sprintf("10.%d.%d.0/24", 100+n/250, n%250)}
			}
			if _, ok := v4["prefixes"]; ok {
				if _, g := v4["gateway"]; !g {
					v4["gateway"] = gatewayOf(v4["prefixes"].([]any)[0].(string))
				}
			}
			delete(v4, "prefix")
			delete(v4, "prefixLength")
			if _, ok := v4["nameservers"]; !ok {
				v4["nameservers"] = []any{}
			}
		}
		return nil
	}
	x.networks.BeforeDel = func(r *http.Request, res store.Resource) (int, string) {
		if len(d.Store.List(store.Filter{Type: "iaas/nic", Project: res.Project, Parent: res.ID})) > 0 {
			return 409, "network still has network interfaces"
		}
		return 0, ""
	}

	x.sgs = base("iaas/security-group", "security group", "securityGroupId")
	x.sgs.Unique = true
	x.sgs.Children = []string{"iaas/security-group-rule"}
	x.sgs.Prepare = func(r *http.Request, s crud.Scope, b map[string]any) error {
		if httpx.Str(b, "name") == "" {
			return errBad("name is required")
		}
		if _, ok := b["stateful"]; !ok {
			b["stateful"] = true
		}
		return nil
	}

	x.rules = base("iaas/security-group-rule", "security group rule", "ruleId")
	x.rules.Scope = func(r *http.Request) crud.Scope {
		s := projScope(r)
		s.Parent = r.PathValue("sgId")
		return s
	}
	x.rules.NameKey = "id"
	x.rules.Prepare = func(r *http.Request, s crud.Scope, b map[string]any) error {
		if !x.exists("iaas/security-group", s.Project, s.Parent) {
			return errBad("security group not found")
		}
		if dir := httpx.Str(b, "direction"); dir != "ingress" && dir != "egress" {
			return errBad("direction must be ingress or egress")
		}
		b["securityGroupId"] = s.Parent
		// the SDK payload may send protocol as a plain string; responses need {name, number}.
		if name, ok := b["protocol"].(string); ok {
			b["protocol"] = map[string]any{"name": name, "number": protoNumbers[name]}
		} else if pm := crud.Map(b["protocol"]); pm != nil && pm["number"] == nil {
			pm["number"] = protoNumbers[httpx.Str(pm, "name")]
		}
		if httpx.Str(b, "ethertype") == "" {
			b["ethertype"] = "IPv4"
		}
		return nil
	}

	x.volumes = base("iaas/volume", "volume", "volumeId")
	x.volumes.StatusKey, x.volumes.Initial, x.volumes.Final, x.volumes.Deleting =
		"status", "CREATING", "AVAILABLE", "DELETING"
	x.volumes.Prepare = func(r *http.Request, s crud.Scope, b map[string]any) error {
		if httpx.Str(b, "availabilityZone") == "" {
			return errBad("availabilityZone is required")
		}
		if _, ok := b["size"]; !ok {
			b["size"] = 64
		}
		if httpx.Str(b, "performanceClass") == "" {
			b["performanceClass"] = "storage_premium_perf1"
		}
		b["bootable"], b["encrypted"] = false, false
		return nil
	}
	x.volumes.BeforeDel = func(r *http.Request, res store.Resource) (int, string) {
		if res.Data["serverId"] != nil {
			return 409, "volume is attached to a server"
		}
		return 0, ""
	}

	x.nics = base("iaas/nic", "network interface", "nicId")
	x.nics.Scope = func(r *http.Request) crud.Scope {
		s := projScope(r)
		s.Parent = r.PathValue("networkId")
		return s
	}
	x.nics.StatusKey, x.nics.Initial = "status", "AVAILABLE"
	x.nics.Prepare = func(r *http.Request, s crud.Scope, b map[string]any) error {
		net, ok := d.Store.Get("iaas/network", s.Project, s.Parent)
		if !ok {
			return errBad("network not found")
		}
		fillNic(b, net)
		return nil
	}
	nicsGet := *x.nics
	nicsGet.Scope = projScope
	x.nicsGet = &nicsGet
	x.nicsGet.BeforeDel = func(r *http.Request, res store.Resource) (int, string) {
		if res.Data["device"] != nil {
			return 409, "network interface is attached to a server"
		}
		return 0, ""
	}

	x.ips = base("iaas/public-ip", "public IP", "publicIpId")
	x.ips.NameKey = "ip"
	x.ips.Prepare = func(r *http.Request, s crud.Scope, b map[string]any) error {
		b["ip"] = fmt.Sprintf("193.148.%d.%d", 160+rand.IntN(32), 1+rand.IntN(253))
		b["networkInterface"] = nil
		return nil
	}
	x.ips.BeforeDel = func(r *http.Request, res store.Resource) (int, string) {
		if res.Data["networkInterface"] != nil {
			return 409, "public IP is still attached"
		}
		return 0, ""
	}

	x.keypairs = base("iaas/keypair", "key pair", "keypairName")
	x.keypairs.IDKey, x.keypairs.KeyKey = "", "name"
	x.keypairs.Scope = func(*http.Request) crud.Scope { return crud.Scope{} }
	x.keypairs.Prepare = func(r *http.Request, s crud.Scope, b map[string]any) error {
		pk := strings.TrimSpace(httpx.Str(b, "publicKey"))
		if pk == "" {
			return errBad("publicKey is required")
		}
		if httpx.Str(b, "name") == "" {
			b["name"] = "keypair-" + crud.Rand(6)
		}
		sum := md5.Sum([]byte(pk))
		hx := hex.EncodeToString(sum[:])
		var parts []string
		for i := 0; i < len(hx); i += 2 {
			parts = append(parts, hx[i:i+2])
		}
		b["fingerprint"] = strings.Join(parts, ":")
		return nil
	}

	x.images = base("iaas/image", "image", "imageId")
	x.images.StatusKey, x.images.Initial, x.images.Deleting = "status", "CREATING", "DELETING"
	x.images.Prepare = func(r *http.Request, s crud.Scope, b map[string]any) error {
		if httpx.Str(b, "name") == "" || httpx.Str(b, "diskFormat") == "" {
			return errBad("name and diskFormat are required")
		}
		b["_uploadUrl"] = x.d.BaseFor(r) + "/upload/images/" + fmt.Sprint(b["id"])
		b["scope"], b["protected"] = "project", false
		return nil
	}
	x.images.CreateResp = func(raw map[string]any) any {
		return map[string]any{"id": raw["id"], "uploadUrl": raw["_uploadUrl"]}
	}

	x.images.ListWrap = func(items []any) any {
		for _, im := range staticImages {
			items = append(items, im)
		}
		return map[string]any{"items": items}
	}

	x.servers = base("iaas/server", "server", "serverId")
	x.servers.Unique = true
	x.servers.StatusKey, x.servers.Initial, x.servers.Final, x.servers.Deleting =
		"status", "CREATING", "ACTIVE", "DELETING"
	x.servers.Prepare = x.prepareServer
	x.servers.Decorate = func(m map[string]any, res store.Resource) {
		m["powerStatus"] = "STOPPED"
		if res.Status == "ACTIVE" {
			m["powerStatus"] = "RUNNING"
		}
	}
	x.areas = &crud.Coll{D: d, Err: httpx.ErrIaaS, Type: "iaas/network-area", Service: "iaas", Label: "network area",
		IDParam: "areaId", IDKey: "id", Stamp: true, Children: []string{"iaas/network-area-region"},
		Scope: func(r *http.Request) crud.Scope { return crud.Scope{Project: r.PathValue("organizationId")} },
		Prepare: func(r *http.Request, s crud.Scope, b map[string]any) error {
			if httpx.Str(b, "name") == "" {
				return errBad("name is required")
			}
			b["projectCount"] = 0
			return nil
		}}
	x.areas.Unique = true

	x.regions = &crud.Coll{D: d, Err: httpx.ErrIaaS, Type: "iaas/network-area-region", Service: "iaas",
		Label: "network area region", IDParam: "region", KeyKey: "_region",
		StatusKey: "status", Initial: "CREATING", Final: "CREATED", Deleting: "DELETING",
		Scope: func(r *http.Request) crud.Scope {
			return crud.Scope{Project: r.PathValue("areaId"), Region: r.PathValue("region"), Parent: r.PathValue("areaId")}
		},
		Prepare: func(r *http.Request, s crud.Scope, b map[string]any) error {
			if !x.exists("iaas/network-area", r.PathValue("organizationId"), s.Parent) {
				return errBad("network area not found")
			}
			b["_region"] = s.Region
			v4 := crud.Map(b["ipv4"])
			if v4 == nil {
				v4 = map[string]any{}
				b["ipv4"] = v4
			}
			for k, v := range map[string]any{"defaultPrefixLen": 25, "maxPrefixLen": 29, "minPrefixLen": 24, "transferNetwork": "100.64.0.0/24"} {
				if _, ok := v4[k]; !ok {
					v4[k] = v
				}
			}
			ranges, _ := v4["networkRanges"].([]any)
			for _, rg := range ranges {
				if m := crud.Map(rg); m != nil && m["id"] == nil {
					m["id"] = httpx.UUID()
				}
			}
			if ranges == nil {
				v4["networkRanges"] = []any{}
			}
			return nil
		}}
	// regions live in the area's own scope (Project = areaId) so keys never collide.
	x.areas.Children = nil
	x.areas.BeforeDel = func(r *http.Request, res store.Resource) (int, string) {
		for _, rg := range d.Store.List(store.Filter{Type: "iaas/network-area-region", Project: res.ID}) {
			d.Store.Delete(rg.Type, rg.Project, rg.ID)
		}
		return 0, ""
	}

	// server removal cleans up auto-created NICs, boot volume and attachments.
	x.servers.BeforeDel = func(r *http.Request, res store.Resource) (int, string) {
		for _, nic := range d.Store.List(store.Filter{Type: "iaas/nic", Project: res.Project}) {
			if nic.Data["device"] != res.ID {
				continue
			}
			if nic.Data["_auto"] == true {
				d.Store.Delete(nic.Type, nic.Project, nic.ID)
			} else {
				d.Store.Update(nic.Type, nic.Project, nic.ID, func(n *store.Resource) { n.Data["device"] = nil })
			}
		}
		for _, v := range d.Store.List(store.Filter{Type: "iaas/volume", Project: res.Project}) {
			if v.Data["serverId"] != res.ID {
				continue
			}
			if bv := crud.Map(res.Data["bootVolume"]); bv != nil && bv["id"] == v.ID && bv["deleteOnTermination"] == true {
				d.Store.Delete(v.Type, v.Project, v.ID)
				continue
			}
			d.Store.Update(v.Type, v.Project, v.ID, func(c *store.Resource) { c.Data["serverId"] = nil; c.Status = "AVAILABLE" })
		}
		for _, ip := range d.Store.List(store.Filter{Type: "iaas/public-ip", Project: res.Project}) {
			if ip.Data["networkInterface"] == res.ID {
				d.Store.Update(ip.Type, ip.Project, ip.ID, func(c *store.Resource) { c.Data["networkInterface"] = nil })
			}
		}
		return 0, ""
	}
}

func gatewayOf(prefix string) string {
	ip := strings.SplitN(prefix, "/", 2)[0]
	p := strings.Split(ip, ".")
	if len(p) != 4 {
		return ""
	}
	return strings.Join(p[:3], ".") + ".1"
}

var nicIP atomic.Int32

func fillNic(b map[string]any, net store.Resource) {
	b["networkId"] = net.ID
	b["mac"] = fmt.Sprintf("fa:16:3e:%02x:%02x:%02x", rand.IntN(256), rand.IntN(256), rand.IntN(256))
	b["type"] = "server"
	if _, ok := b["nicSecurity"]; !ok {
		b["nicSecurity"] = true
	}
	if _, ok := b["securityGroups"]; !ok {
		b["securityGroups"] = []any{}
	}
	if httpx.Str(b, "ipv4") == "" {
		pfx := "10.0.0.0/24"
		if v4 := crud.Map(net.Data["ipv4"]); v4 != nil {
			if ps, _ := v4["prefixes"].([]any); len(ps) > 0 {
				pfx, _ = ps[0].(string)
			}
		}
		p := strings.Split(strings.SplitN(pfx, "/", 2)[0], ".")
		if len(p) == 4 {
			b["ipv4"] = fmt.Sprintf("%s.%s.%s.%d", p[0], p[1], p[2], 10+int(nicIP.Add(1))%200)
		}
	}
}

func (x *h) listAreaRegions(w http.ResponseWriter, r *http.Request) {
	org, area := r.PathValue("organizationId"), r.PathValue("areaId")
	if !x.exists("iaas/network-area", org, area) {
		httpx.ErrIaaS(w, r, 404, "network area not found")
		return
	}
	out := map[string]any{}
	for _, rg := range x.d.Store.List(store.Filter{Type: "iaas/network-area-region", Project: area}) {
		out[rg.Region] = x.regions.View(rg)
	}
	httpx.WriteJSON(w, 200, map[string]any{"regions": out})
}

func (x *h) prepareServer(r *http.Request, s crud.Scope, b map[string]any) error {
	if httpx.Str(b, "name") == "" || httpx.Str(b, "machineType") == "" {
		return errBad("name and machineType are required")
	}
	if httpx.Str(b, "availabilityZone") == "" {
		b["availabilityZone"] = s.Region + "-1"
	}
	nw := crud.Map(b["networking"])
	if nw == nil {
		return errBad("networking is required")
	}
	var nics []any
	add := func(nic store.Resource) {
		net, _ := x.d.Store.Get("iaas/network", s.Project, nic.Parent)
		nics = append(nics, map[string]any{
			"nicId": nic.ID, "networkId": nic.Parent, "networkName": net.Name, "mac": nic.Data["mac"],
			"nicSecurity": nic.Data["nicSecurity"], "ipv4": nic.Data["ipv4"], "securityGroups": []any{},
		})
		x.d.Store.Update(nic.Type, nic.Project, nic.ID, func(n *store.Resource) { n.Data["device"] = b["id"] })
	}
	if nid := httpx.Str(nw, "networkId"); nid != "" {
		net, ok := x.d.Store.Get("iaas/network", s.Project, nid)
		if !ok {
			return errBad("network " + nid + " not found")
		}
		nb := map[string]any{"id": httpx.UUID(), "name": httpx.Str(b, "name") + "-nic", "_auto": true}
		fillNic(nb, net)
		res, err := x.d.Store.Create(store.Resource{Type: "iaas/nic", Service: "iaas", ID: nb["id"].(string),
			Project: s.Project, Region: s.Region, Parent: nid, Name: nb["name"].(string), Status: "AVAILABLE", Data: nb})
		if err != nil {
			return err
		}
		add(res)
	} else if ids, ok := nw["nicIds"].([]any); ok {
		for _, id := range ids {
			nic, found := x.d.Store.Get("iaas/nic", s.Project, fmt.Sprint(id))
			if !found {
				return errBad("network interface " + fmt.Sprint(id) + " not found")
			}
			add(nic)
		}
	} else {
		return errBad("networking requires networkId or nicIds")
	}
	delete(b, "networking")
	b["nics"] = nics
	b["volumes"] = []any{}
	if _, ok := b["securityGroups"]; !ok {
		b["securityGroups"] = []any{}
	}
	if bv := crud.Map(b["bootVolume"]); bv != nil {
		vid := httpx.UUID()
		bv["id"] = vid
		if _, ok := bv["deleteOnTermination"]; !ok {
			bv["deleteOnTermination"] = false
		}
		if _, ok := bv["size"]; !ok {
			bv["size"] = 64
		}
		if bv["performanceClass"] == nil {
			bv["performanceClass"] = "storage_premium_perf1"
		}
		vb := map[string]any{"id": vid, "name": httpx.Str(b, "name") + "-boot", "size": bv["size"],
			"availabilityZone": b["availabilityZone"], "bootable": true, "serverId": b["id"],
			"performanceClass": bv["performanceClass"], "source": bv["source"], "encrypted": false}
		if _, err := x.d.Store.Create(store.Resource{Type: "iaas/volume", Service: "iaas", ID: vid, Project: s.Project,
			Region: s.Region, Name: vb["name"].(string), Status: "ATTACHED", Data: vb}); err != nil {
			return err
		}
	}
	return nil
}

func (x *h) serverGone(w http.ResponseWriter, r *http.Request) (store.Resource, bool) {
	res, ok := x.d.Store.Get("iaas/server", r.PathValue("projectId"), r.PathValue("serverId"))
	if !ok {
		httpx.ErrIaaS(w, r, 404, "server not found")
	}
	return res, ok
}

func (x *h) serverAction(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, ok := x.serverGone(w, r)
		if !ok {
			return
		}
		x.d.Store.Update(res.Type, res.Project, res.ID, func(s *store.Resource) { s.Status = status })
		w.WriteHeader(http.StatusAccepted)
	}
}

func (x *h) serverResize(w http.ResponseWriter, r *http.Request) {
	res, ok := x.serverGone(w, r)
	if !ok {
		return
	}
	b, ok := httpx.Decode(w, r)
	if !ok {
		return
	}
	mt := httpx.Str(b, "machineType")
	if mt == "" {
		httpx.ErrIaaS(w, r, 400, "machineType is required")
		return
	}
	x.d.Store.Update(res.Type, res.Project, res.ID, func(s *store.Resource) { s.Data["machineType"] = mt })
	x.d.Engine.Transition(res.Type, res.Project, res.ID, "RESIZING", res.Status)
	w.WriteHeader(http.StatusAccepted)
}

func (x *h) volumeResize(w http.ResponseWriter, r *http.Request) {
	b, ok := httpx.Decode(w, r)
	if !ok {
		return
	}
	_, found := x.d.Store.Update("iaas/volume", r.PathValue("projectId"), r.PathValue("volumeId"), func(v *store.Resource) {
		if sz, ok := b["size"]; ok {
			v.Data["size"] = sz
		}
	})
	if !found {
		httpx.ErrIaaS(w, r, 404, "volume not found")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// attach implements PUT/DELETE /servers/{id}/<kind>/{attId}.
func (x *h) attach(kind string, add bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		srv, ok := x.serverGone(w, r)
		if !ok {
			return
		}
		project, att := srv.Project, r.PathValue("attId")
		var childType, listKey string
		switch kind {
		case "nics":
			childType, listKey = "iaas/nic", "nics"
		case "public-ips":
			childType = "iaas/public-ip"
		case "volume-attachments":
			childType, listKey = "iaas/volume", "volumes"
		case "security-groups":
			childType, listKey = "iaas/security-group", "securityGroups"
		case "service-accounts":
			listKey = "serviceAccountMails"
		}
		var child store.Resource
		if childType != "" {
			if child, ok = x.d.Store.Get(childType, project, att); !ok {
				httpx.ErrIaaS(w, r, 404, kind+" "+att+" not found")
				return
			}
		}
		x.d.Store.Update(srv.Type, srv.Project, srv.ID, func(s *store.Resource) {
			switch kind {
			case "public-ips":
				// first NIC carries the public IP
				if nics, _ := s.Data["nics"].([]any); len(nics) > 0 {
					if n := crud.Map(nics[0]); n != nil {
						if add {
							n["publicIp"] = child.Data["ip"]
						} else {
							delete(n, "publicIp")
						}
					}
				}
			case "nics":
				if add {
					net, _ := x.d.Store.Get("iaas/network", project, child.Parent)
					s.Data[listKey] = append(listOf(s.Data[listKey]), map[string]any{"nicId": att, "networkId": child.Parent,
						"networkName": net.Name, "mac": child.Data["mac"], "nicSecurity": child.Data["nicSecurity"], "ipv4": child.Data["ipv4"]})
				} else {
					s.Data[listKey] = without(listOf(s.Data[listKey]), func(v any) bool { return crud.Map(v)["nicId"] == att })
				}
			default:
				if add {
					s.Data[listKey] = append(without(listOf(s.Data[listKey]), func(v any) bool { return v == att }), att)
				} else {
					s.Data[listKey] = without(listOf(s.Data[listKey]), func(v any) bool { return v == att })
				}
			}
		})
		// reverse links on the child
		switch kind {
		case "nics":
			x.d.Store.Update(child.Type, child.Project, child.ID, func(c *store.Resource) { c.Data["device"] = ifElse(add, srv.ID) })
		case "public-ips":
			x.d.Store.Update(child.Type, child.Project, child.ID, func(c *store.Resource) { c.Data["networkInterface"] = ifElse(add, srv.ID) })
		case "volume-attachments":
			x.d.Store.Update(child.Type, child.Project, child.ID, func(c *store.Resource) {
				c.Data["serverId"] = ifElse(add, srv.ID)
				c.Status = map[bool]string{true: "ATTACHED", false: "AVAILABLE"}[add]
			})
		}
		if kind == "volume-attachments" {
			reqID := httpx.UUID()
			action, status := "CREATE", "CREATED"
			if !add {
				action, status = "DELETE", "DELETED"
			}
			x.requests.Store(reqID, map[string]any{"requestId": reqID, "requestAction": action, "requestType": "ServerVolumeAttachment",
				"status": status, "resources": []any{map[string]any{"id": att, "status": status, "type": "volume"}}})
			w.Header().Set("X-Request-Id", reqID)
			if add {
				httpx.WriteJSON(w, 200, map[string]any{"id": att, "volumeId": att, "serverId": srv.ID, "deleteOnTermination": false})
				return
			}
		}
		if add {
			httpx.WriteJSON(w, 200, map[string]any{})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func ifElse(add bool, v string) any {
	if add {
		return v
	}
	return nil
}

func listOf(v any) []any { l, _ := v.([]any); return l }

func without(l []any, drop func(any) bool) []any {
	out := []any{}
	for _, v := range l {
		if !drop(v) {
			out = append(out, v)
		}
	}
	return out
}

func (x *h) getVolumeAttachment(w http.ResponseWriter, r *http.Request) {
	srv, ok := x.serverGone(w, r)
	if !ok {
		return
	}
	vol := r.PathValue("attId")
	for _, v := range listOf(srv.Data["volumes"]) {
		if v == vol {
			httpx.WriteJSON(w, 200, map[string]any{"id": vol, "volumeId": vol, "serverId": srv.ID, "deleteOnTermination": false})
			return
		}
	}
	httpx.ErrIaaS(w, r, 404, "volume attachment not found")
}

func (x *h) getRequest(w http.ResponseWriter, r *http.Request) {
	v, ok := x.requests.Load(r.PathValue("requestId"))
	if !ok {
		httpx.ErrIaaS(w, r, 404, "request not found")
		return
	}
	httpx.WriteJSON(w, 200, v)
}

func (x *h) uploadImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("imageId")
	var found store.Resource
	for _, img := range x.d.Store.List(store.Filter{Type: "iaas/image"}) {
		if img.ID == id {
			found = img
		}
	}
	if found.ID == "" {
		httpx.ErrIaaS(w, r, 404, "image not found")
		return
	}
	n, _ := io.Copy(io.Discard, r.Body)
	x.d.Engine.Transition(found.Type, found.Project, found.ID, "SAVING", "AVAILABLE")
	x.d.Store.Update(found.Type, found.Project, found.ID, func(i *store.Resource) { i.Data["size"] = n })
	w.WriteHeader(http.StatusOK)
}

func (x *h) getImage(w http.ResponseWriter, r *http.Request) {
	for _, im := range staticImages {
		if im["id"] == r.PathValue("imageId") {
			httpx.WriteJSON(w, 200, im)
			return
		}
	}
	x.images.Get(w, r)
}

var staticImages = []map[string]any{
	staticImage("a0000000-0000-4000-8000-000000000001", "Ubuntu 24.04", "ubuntu", "24.04"),
	staticImage("a0000000-0000-4000-8000-000000000002", "Ubuntu 22.04", "ubuntu", "22.04"),
	staticImage("a0000000-0000-4000-8000-000000000003", "Debian 12", "debian", "12"),
	staticImage("a0000000-0000-4000-8000-000000000004", "Flatcar 4081", "flatcar", "4081"),
}

func staticImage(id, name, distro, version string) map[string]any {
	return map[string]any{"id": id, "name": name, "status": "AVAILABLE", "scope": "public", "diskFormat": "qcow2",
		"protected": true, "minDiskSize": 8, "minRam": 512, "owner": "stackit",
		"labels": map[string]any{"distro": distro, "version": version}}
}

var machineTypes = func() []map[string]any {
	var out []map[string]any
	for _, fam := range []struct {
		prefix string
		ratio  int
	}{{"c2i", 2}, {"g2i", 4}, {"m2i", 8}} {
		for _, v := range []int{1, 2, 4, 8, 16} {
			out = append(out, map[string]any{"name": fmt.Sprintf("%s.%d", fam.prefix, v), "vcpus": v,
				"ram": v * fam.ratio * 1024, "disk": 0, "description": "mock " + fam.prefix, "extraSpecs": map[string]any{}})
		}
	}
	return out
}()
