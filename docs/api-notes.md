# STACKIT API notes for the mock server

Endpoint and field reference for a Go mock of the STACKIT REST APIs, so that the
Terraform provider `stackitcloud/stackit` works against it.

## Sources and versions

Read from shallow clones (2026-11-10) of:

- `stackitcloud/stackit-sdk-go` at HEAD. Abbreviated `SDK/` below.
- `stackitcloud/terraform-provider-stackit` at HEAD (commit `dbf5804`). Abbreviated `TF/` below.

The SDK service versions at HEAD equal the versions pinned in the provider `go.mod`
(iaas v1.15.1, resourcemanager v0.28.0, serviceaccount v0.22.0, ske v1.22.0,
postgresflex v1.16.0, mongodbflex v1.13.0, serviceenablement v1.8.0, core v0.27.1).

API versions the provider actually uses (`TF/stackit/internal/core/clientutils.go`):

| Service | SDK package | URL prefix |
|---|---|---|
| Resource Manager | `services/resourcemanager/v0api` | `/v2` |
| Service Account | `services/serviceaccount/v2api` | `/v2` (token: `/token`) |
| IaaS | `services/iaas/v2api` (plus `v2alpha1api` for beta VPC resources, not covered) | `/v2` |
| SKE | `services/ske/v2api` | `/v2` |
| Service Enablement | `services/serviceenablement/v2api` | `/v2` (needed by SKE, see below) |
| PostgreSQL Flex | `services/postgresflex/v3api` (not v2) | `/v3` |
| MongoDB Flex | `services/mongodbflex/v2api` | `/v2` |

Statements marked **[UNCERTAIN]** could not be verified from the code.

---

## 0. Cross-cutting behaviour of the generated clients

All of this is in `SDK/services/<svc>/<ver>/api_default.go` and `client.go`.

1. **Status codes.** Every `...Execute()` treats `StatusCode >= 300` as an error and everything below 300
   as success. 200, 201, 202 and 204 are all fine. The exact success code the real API returns is not
   encoded in the SDK.
   **[UNCERTAIN]** Real APIs typically answer 202 for async creates and deletes. Any 2xx works for the SDK.
2. **Response decode.** Only if the body is non-empty:
   - `Content-Type` must match `(?i:(?:application|text)/(?:[^;]+\+)?json)` (`client.go`, `JsonCheck`).
   - Otherwise the client fails with `undefined response type`.
   - An empty body is accepted and gives a zero value. A `Create` answered with an empty body therefore
     yields an empty id.
3. **Required properties are enforced.** Each generated model has a custom `UnmarshalJSON` that fails with
   `no value given for required property X` if a required key is missing. A key present with `null` counts as
   present. This is the biggest trap: the mock must send all required fields of every response model.
   Required fields are listed per model below (marked `*`).
4. **Time fields** (`time.Time`) must be RFC 3339 strings, for example `2026-11-10T12:00:00Z`.
5. **Unknown enum values.** `LifecycleState` and similar generated enums map unknown strings to
   `unknown_default_open_api` instead of failing (`resourcemanager/v0api/model_lifecycle_state.go`).
   IaaS `status` fields are plain strings.
6. **Retry.** The provider wraps every client in a transport that retries only HTTP 429
   (`TF/stackit/internal/core/clientutils.go`, `RetryTransport` and `defaultConfigOptions`). The wait handlers
   retry 502 and 504 up to 5 times (`SDK/core/wait/wait.go`, `RetryHttpErrorStatusCodes`).
7. **Auth header.** Token flow sets `Authorization: Bearer <token>` (`SDK/core/clients/token_flow.go`).
   The mock can ignore it.
8. **Path-parameter collision.** All services share one host if you point everything at `localhost:8080`.
   `GET /v2/projects/{id}` exists in both Resource Manager (`GetProject`) and IaaS (`GetProjectDetails`).
   Prefer a path prefix per service (see section 10).
9. **IDs.**
   - Provider schemas validate `project_id` as a UUID (`validate.UUID()` in the iaas resource schemas, e.g. `iaas/network/resource.go` L226) and organization ids likewise.
     Use UUIDs for project, organization, server, volume, network and similar ids.
   - Postgres Flex `userId` and `databaseId` are **int64** (path params typed `int64` in
     `postgresflex/v3api/api_default.go`).
   - Resource Manager `containerId` is a user-chosen string. `GET /v2/projects/{id}` accepts container id or UUID.

### Wait-handler engine (`SDK/core/wait/wait.go`, `waiterhelper.go`)

- The first check runs immediately after `sleepBeforeWait`. Later checks run every **5 s** (default throttle).
  A mock that already returns the final state on the first GET avoids the 5 s ticks.
- `WaiterHelper.Wait()` semantics:
  - GET returns an error:
    - If `ActiveState` is **empty** (a delete waiter) and the status code is in `DeleteHttpErrorStatusCodes`,
      the wait is done successfully. The default list is **403, 404, 410**.
    - Otherwise the error is returned (the wait aborts, except for 502/504 retries).
    - A create waiter therefore aborts on a 404, so the resource must be GETtable right after the create call.
  - State in `ActiveState`: done.
  - State in `ErrorState`: error `waiting failed. state is X`.
  - Any other state: still in progress.
- For a delete waiter, a GET that keeps returning the object with a non-error state means "still deleting" until
  the 30 min or 15 min timeout.

### Fixed sleeps before polling (unavoidable in tests)

- Resource Manager project create: **1 min** (`resourcemanager/v0api/wait/wait.go`).
- Service Enablement enable: **15 s** (`serviceenablement/v2api/wait/wait.go`).
- Postgres Flex instance create: 15 s.
- MongoDB Flex instance create: 5 s.
- IaaS network create, update and network-area-region create/delete: 2 s.
- Service account create: provider `time.Sleep(5s)` (`TF/.../serviceaccount/account/resource.go`).

---

## 1. Resource Manager (projects)

SDK: `services/resourcemanager/v0api/api_default.go`. Base: `https://resource-manager.api.stackit.cloud`.
Provider resource: `TF/stackit/internal/services/resourcemanager/project/resource.go`.

| Op | Method and path | Notes |
|---|---|---|
| CreateProject | `POST /v2/projects` | Body `CreateProjectPayload`. Returns `Project`. |
| GetProject | `GET /v2/projects/{id}` | `id` = containerId or UUID. Returns `GetProjectResponse`. |
| PartialUpdateProject | `PATCH /v2/projects/{id}` | Returns `Project`. |
| DeleteProject | `DELETE /v2/projects/{id}` | No body needed. |
| ListProjects | `GET /v2/projects` | `ListProjectsResponse`, data sources only. |
| Folders and organizations | `/v2/folders...`, `/v2/organizations...` | Not needed for the project resource. |

JSON (`*` = required, otherwise the decode fails):

- `CreateProjectPayload` (request): `containerParentId*`, `name*` (regex `^[a-zA-ZäüöÄÜÖ0-9]( ?[a-zA-ZäüöÄÜÖß0-9_+&-]){1,40}$`),
  `labels` (map), `members` (`[{role*, subject*}]`).
- `Project` (create response): `containerId*`, `projectId*`, `name*`, `lifecycleState*`, `creationTime*`,
  `updateTime*`, `parent*` (`{containerId*, id*, type*}`), `labels`.
- `GetProjectResponse`: same as `Project` plus optional `parents[]`. Required: `containerId`, `creationTime`,
  `lifecycleState`, `name`, `parent`, `projectId`, `updateTime`.
- `parent.type` enum: `ORGANIZATION`, `FOLDER`.
- `lifecycleState` enum (`model_lifecycle_state.go`): `CREATING`, `ACTIVE`, `DELETING`, `INACTIVE`.
- `PartialUpdateProjectPayload`: `name`, `labels` (map string to nullable string), `containerParentId`.

Provider behaviour:

- The provider reads `containerId` from the create response and stores it, then waits.
- If `container_parent_id` in the config parses as a UUID, the state takes `parent.id`, else `parent.containerId`.
  Echo both.
- Read: HTTP **403** on GET removes the resource from state (404 does not; it is an error).

Wait handlers (`resourcemanager/v0api/wait/wait.go`):

- `CreateProjectWaitHandler`:
  - Sleeps 1 min first, then GET until `lifecycleState == ACTIVE`.
  - `INACTIVE` is an error.
  - `CREATING` or `DELETING` means in progress.
  - Timeout 45 min.
- `DeleteProjectWaitHandler`:
  - Done when GET returns **404 or 403 only** (`DeleteHttpErrorStatusCodes` is overridden to `[404, 403]`,
    so **410 would be an error**).
  - `INACTIVE` is an error.
  - Timeout 15 min.

Error body (`model_error_response.go`, decoded for 400/403/409):
`{"error":"Bad Request","message":"...","path":"/v2/projects","status":400,"timeStamp":"2026-11-10T12:00:00Z"}`
(all five keys required for typed decode; if missing, the error still carries status and body).

---

## 2. Service Account

SDK: `services/serviceaccount/v2api/api_default.go`. Base: `https://service-account.api.stackit.cloud`.
Provider: `TF/stackit/internal/services/serviceaccount/{account,key}/resource.go`.

| Op | Method and path |
|---|---|
| CreateServiceAccount | `POST /v2/projects/{projectId}/service-accounts` |
| ListServiceAccounts | `GET /v2/projects/{projectId}/service-accounts` |
| DeleteServiceAccount | `DELETE /v2/projects/{projectId}/service-accounts/{serviceAccountEmail}` |
| CreateServiceAccountKey | `POST /v2/projects/{projectId}/service-accounts/{serviceAccountEmail}/keys` |
| GetServiceAccountKey | `GET .../keys/{keyId}` |
| ListServiceAccountKeys | `GET .../keys` |
| PartialUpdateServiceAccountKey | `PATCH .../keys/{keyId}` |
| DeleteServiceAccountKey | `DELETE .../keys/{keyId}` |
| Federated identity providers | `.../federations[/{federationId}]` (POST, GET, PATCH, DELETE) |
| JWKS | `GET /v2/service-accounts/public/jwk/{serviceAccountEmail}` |
| Token (key flow) | `POST /token` (see section 9) |

There is **no GetServiceAccount**. The provider resource reads via `ListServiceAccounts` and matches by `email`.
If no match is found, the resource is removed from state.

JSON:

- `CreateServiceAccountPayload` (request): `name*`.
- `ServiceAccount` (create response and list item): `id*`, `email*`, `projectId*`, `internal*` (bool).
  Generate the email as `<name>-<random>@sa.stackit.cloud`.
- `ListServiceAccountsResponse`: `{"items*": [ServiceAccount]}`.
- `CreateServiceAccountKeyPayload` (request): optional `validUntil` (RFC 3339), `publicKey`, `algorithm`.
  The provider sends `validUntil` when `ttl_days` is set, and `publicKey` if set.
- `CreateServiceAccountKeyResponse`:
  - `id*`, `active*`, `createdAt*`, `publicKey*`, `keyAlgorithm*`, `keyOrigin*`, `keyType*`,
    `credentials*`, `validUntil`.
  - `credentials`: `aud*`, `iss*`, `kid*`, `sub*`, `privateKey`, `tokenEndpoint`.
  - The provider stores the **whole create response JSON** in its `json` attribute and takes `id` as `key_id`.
- Key enums (all with an `unknown_default_open_api` fallback):

  | Field | Values |
  |---|---|
  | `keyAlgorithm` | `RSA_2048`, `RSA_4096` |
  | `keyOrigin` | `USER_PROVIDED`, `GENERATED` |
  | `keyType` | `USER_MANAGED`, `SYSTEM_MANAGED` |

- `GetServiceAccountKeyResponse`: required `id`, `active`, `createdAt`, `keyAlgorithm`, `keyOrigin`,
  `keyType` and `credentials{aud,iss,kid,sub}`. The provider only checks that the call succeeds.
  GET key answering **404, 403 or 400** removes the key from state.
- `ListServiceAccountKeysResponse`: `{"items*": [ServiceAccountKeyListResponse]}`, where each item has the same
  required fields as the create response minus `credentials` and `publicKey`.

No wait handlers. Delete is a plain DELETE (a 404 on DELETE is tolerated for both account and key).

Error bodies (`model_error.go`, `model_auth_error*.go`):

- `Error` (400, 404 and others): `{"error*","message*","path*","status*" (int),"timeStamp*"}`.
- `AuthError` (401/403): `{"error":{"code":401,"message":"...","status":"UNAUTHENTICATED"}}`.

---

## 3. IaaS (v2api)

SDK: `services/iaas/v2api/api_default.go`. Base: `https://iaas.api.stackit.cloud`.
Let `R = /v2/projects/{projectId}/regions/{region}`. The region value is typically `eu01`.

### Endpoints used by the provider

| Resource | Endpoints |
|---|---|
| Network | `POST R/networks`, `GET R/networks/{networkId}`, `PATCH R/networks/{networkId}`, `DELETE R/networks/{networkId}`, `GET R/networks` |
| Security group | `POST R/security-groups`, `GET R/security-groups/{id}`, `PATCH ...`, `DELETE ...`, `GET R/security-groups` |
| Security group rule | `POST R/security-groups/{sgId}/rules`, `GET .../rules/{ruleId}`, `DELETE .../rules/{ruleId}`, `GET .../rules` |
| Server | `POST R/servers`, `GET R/servers/{serverId}` (query `details=true`), `PATCH`, `DELETE`, `GET R/servers`, `POST R/servers/{id}/start`, `/stop`, `/deallocate`, `/resize`, `/reboot`, `/rescue`, `/unrescue` |
| Server attachments | `PUT R/servers/{id}/nics/{nicId}`, `PUT R/servers/{id}/public-ips/{ipId}`, `PUT R/servers/{id}/volume-attachments/{volId}`, `PUT R/servers/{id}/security-groups/{sgId}`, `PUT R/servers/{id}/service-accounts/{mail}`, with matching `DELETE`s and `GET R/servers/{id}/volume-attachments[/{volId}]` |
| Volume | `POST R/volumes`, `GET R/volumes/{volumeId}`, `PATCH`, `DELETE`, `POST R/volumes/{id}/resize`, `GET R/volumes` |
| Public IP | `POST R/public-ips`, `GET R/public-ips/{publicIpId}`, `PATCH`, `DELETE`, `GET R/public-ips` |
| NIC | `POST R/networks/{networkId}/nics`, `GET R/networks/{networkId}/nics/{nicId}`, `PATCH`, `DELETE`, `GET R/networks/{networkId}/nics`, `GET R/nics/{nicId}`, `GET R/nics` |
| Key pair (global) | `POST /v2/keypairs`, `GET /v2/keypairs/{keypairName}`, `PATCH`, `DELETE`, `GET /v2/keypairs` |
| Image | `POST R/images` (returns `uploadUrl`), `GET R/images/{imageId}`, `PATCH`, `DELETE`, `GET R/images` (query `all`, `label_selector`) |
| Machine type | `GET R/machine-types`, `GET R/machine-types/{machineType}` |
| Request status | `GET R/requests/{requestId}` (volume attach/detach) |
| Availability zones | `GET /v2/regions/{region}/availability-zones` |
| Network area (org level) | See below. `O = /v2/organizations/{organizationId}` |

Network area (`stackit_network_area` and `..._region`):

- `POST O/network-areas`, `GET O/network-areas/{areaId}`, `PATCH`, `DELETE`, `GET O/network-areas`.
- `GET O/network-areas/{areaId}/projects` (used before delete; must return `{"items":[]}`, not null).
- `GET O/network-areas/{areaId}/regions` returns `{"regions": {"eu01": {...}}}`.
- `PUT|GET|PATCH|DELETE O/network-areas/{areaId}/regions/{region}`.
- `POST|GET|DELETE O/network-areas/{areaId}/regions/{region}/network-ranges[/{id}]`.
- Routes and routing tables live under `.../regions/{region}/routes` and `.../routing-tables`.

All of these are in `iaas/v2api/api_default.go`.

### Models (`iaas/v2api/model_*.go`, `*` = required)

- `Network`: `id*`, `name*`, `status*`, `dhcp`, `routed`, `description`, `labels` (map), `routingTableId`, `vpcId`,
  `createdAt`, `updatedAt`.
  - `ipv4`: `{prefixes*: [..], gateway (nullable string), nameservers[], publicIp, vpcNetworkRangeId}`.
  - `ipv6`: same shape.
  - The provider reads `ipv4.nameservers`, `.prefixes`, `.gateway`, `.publicIp`, `.vpcNetworkRangeId`. All are
    nil-tolerant (`network/resource.go` `mapFields`).
- `CreateNetworkPayload` (request): `name*`, `dhcp`, `routed`, `labels`, `routingTableId`, `vpcId`.
  - `ipv4` is one of `{prefix*, gateway, nameservers, vpcNetworkRangeId}` or `{prefixLength*, nameservers, ...}`.
  - For `prefixLength` the mock must allocate a prefix and return it in `ipv4.prefixes`.
- `NetworkArea`: `name*`, `id`, `labels`, `projectCount`, `createdAt`, `updatedAt`.
- `RegionalArea` (for `/regions/{region}`):
  `{status, ipv4: {defaultPrefixLen*, maxPrefixLen*, minPrefixLen*, networkRanges*: [{id, prefix*}], transferNetwork*, defaultNameservers[]}}`.
- `RegionalAreaListResponse`: `{"regions*": {"eu01": RegionalArea}}`.
- `NetworkRangeListResponse`: `{"items*": [{id, prefix*}]}`.
- `SecurityGroup`: `name*`, `id`, `description`, `stateful`, `labels`, `rules[]`, `createdAt`, `updatedAt`.
  List: `{"items*": [...]}`.
- `SecurityGroupRule`: `direction*` (`ingress` or `egress`), `id`, `ethertype`, `ipRange`, `remoteSecurityGroupId`,
  `securityGroupId`, `description`, `portRange {min*, max*}`, `icmpParameters {type*, code*}`,
  `protocol {name, number}`. List: `{"items*": [...]}`.
- `Server`: `name*`, `machineType*`, `id`, `status`, `powerStatus`, `availabilityZone`, `imageId`, `keypairName`,
  `bootVolume {id, deleteOnTermination, performanceClass, size, source{id*,type*}}`, `volumes[]` (ids),
  `nics[]` (`ServerNetwork`: `nicId*`, `networkId*`, `networkName*`, `mac*`, `nicSecurity*`, `ipv4`, `publicIp`,
  `securityGroups`), `securityGroups[]`, `serviceAccountMails[]`, `affinityGroup`, `labels`, `metadata`, `userData`,
  `agent`, `maintenanceWindow {startsAt*, endsAt*, status*}`, `launchedAt`, `errorMessage`, `createdAt`, `updatedAt`.
  List: `{"items*": [...]}`.
  - The provider reads `status`. `id` and `status` must be non-nil in the server wait handlers, or the wait aborts
    with `the id or the status are missing`.
- `CreateServerPayload` (request) additionally requires `networking*`, either `{networkId}` or `{nicIds:[..]}`.
- `Volume`: `availabilityZone*`, `id`, `name`, `size`, `status`, `performanceClass`, `bootable`, `serverId`,
  `source {id*,type*}`, `encrypted`, `labels`, `description`. List: `{"items*": [...]}`.
- `PublicIp`: `id`, `ip`, `networkInterface` (nullable string), `labels`. List: `{"items*": [...]}`. The mock must
  assign `ip` on create.
- `NIC`: `id`, `name`, `networkId`, `mac`, `ipv4`, `ipv6`, `nicSecurity`, `securityGroups[]`, `allowedAddresses[]`,
  `type`, `device`, `status`, `labels`. List: `{"items*": [...]}`.
- `Keypair`: `publicKey*`, `name`, `fingerprint`, `labels`, `createdAt`, `updatedAt`. List: `{"items*": [...]}`.
- `Image`: `name*`, `diskFormat*`, `id`, `status`, `scope`, `protected`, `minDiskSize`, `minRam`, `size`, `owner`,
  `labels`, `config {...}`, `agent`, `checksum {algorithm*, digest*}`. List: `{"items*": [...]}`.
- `ImageCreateResponse`: `{"id*","uploadUrl*"}`.
- `MachineType`: `name*`, `vcpus*`, `ram*`, `disk*`, `description`, `extraSpecs` (map). List: `{"items*": [...]}`.
- `Request` (for `GET R/requests/{id}`): `requestId*`, `requestAction*` (`CREATE`, `UPDATE`, `DELETE`),
  `requestType*`, `status*`, `resources*: [{id*,status*,type*}]`, `details`.
- `Project` (`GetProjectDetails`, data source only): `id*`, `areaId*`, `status*`, ...

Image upload (`TF/.../iaas/image/resource.go` `uploadImage`):

- The provider does a **raw `PUT` to the `uploadUrl`** returned by `POST R/images`.
- The request has `Content-Type: application/octet-stream`, and the call uses a plain `http.Client`.
- It requires **status exactly 200**.
- The mock must serve that URL, for example `http://localhost:8080/upload/<imageId>`.

Volume attach and detach (`volumeattach/resource.go`):

- The provider reads the response header **`X-Request-Id`** of `PUT .../volume-attachments/{volId}` (and of the
  matching DELETE). The call fails without it (`no XRequestID header found in response`).
- It then polls `GET R/requests/{requestId}`.
- Create is done when `requestAction == "CREATE"` and `status == "CREATED"`.
  Delete is done when `requestAction == "DELETE"` and `status == "DELETED"`.
  `UPDATE` pairs with `UPDATED`.
  `status == "FAILED"` is an error. An unknown action is an error.
  `requestId` in the body must equal the id requested.

### Wait handlers (`iaas/v2api/wait/wait.go`)

| Handler | GET | Done | Error | Delete done on |
|---|---|---|---|---|
| CreateNetwork / UpdateNetwork | network | `status == "CREATED"` | `FAILED` | |
| DeleteNetwork | network | | `FAILED` | GET 403/404/410 |
| CreateVolume | volume | `status == "AVAILABLE"` | `ERROR` | |
| DeleteVolume | volume | | `ERROR` | GET 403/404/410 |
| CreateServer / Start / Unrescue | server | `status == "ACTIVE"` | `ERROR` | |
| StopServer | server | `INACTIVE` | `ERROR` | |
| DeallocateServer | server | `DEALLOCATED` | `ERROR` | |
| RescueServer | server | `RESCUE` | `ERROR` | |
| ResizeServer | server | needs an intermediate `RESIZING` first, then `ACTIVE` or `INACTIVE` | `ERROR` | |
| DeleteServer | server | | `ERROR` | GET 403/404/410 |
| UploadImage | image | `status == "AVAILABLE"` | `ERROR` | |
| DeleteImage | image | | `ERROR` | GET 403/404/410 |
| CreateNetworkAreaRegion | area region | `status == "CREATED"` | `FAILED` | |
| DeleteNetworkAreaRegion | area region | | `FAILED` | GET **400 or 404** |
| ReadyForNetworkAreaDeletion | `GET O/network-areas/{id}/projects` | `items` empty | | |
| AddVolumeToServer | `GET R/servers/{s}/volume-attachments/{v}` | 200 with `volumeId == v` | | |
| RemoveVolumeFromServer | same GET | | | GET **404** |
| ProjectRequest | `GET R/requests/{id}` | see above | `FAILED` | |

Notes:

- Timeouts: 15 to 30 min. First poll immediate (2 s pre-sleep for network and area-region).
- An unknown status means in progress.
- For delete waiters, the mock should return **404** on the GET after the DELETE.
  403 and 410 would also be accepted, but 404 is what the provider's read paths expect.
- Security group, security group rule, public IP, NIC and key pair have **no wait handlers**. The provider takes
  the create response directly, and later reads use plain GET. A DELETE followed by GET must return 404.
- The Create responses for these resources must contain the generated `id` (rule id, `ip` for public IPs, and so on).
- Provider read paths treat **404** as "gone" (`StatusNotFound` on all iaas resources).
- Network area region read: `utils.IsNetworkAreaRegionNotFound` treats 404, or 400 with body containing
  `region not enabled`, as gone (`TF/.../iaas/utils/util.go`).

Error body (`model_error.go`): `{"code": 404, "msg": "..."}` (both required). Statuses 400, 401, 403, 404 and 500
are decoded into it.

---

## 4. SKE (`ske/v2api`)

SDK: `services/ske/v2api/api_default.go`. Base: `https://ske.api.stackit.cloud`.
Provider: `TF/stackit/internal/services/ske/{cluster,kubeconfig,provideroptions}`.

| Op | Method and path |
|---|---|
| CreateOrUpdateCluster | `PUT /v2/projects/{projectId}/regions/{region}/clusters/{clusterName}` |
| GetCluster | `GET .../clusters/{clusterName}` |
| DeleteCluster | `DELETE .../clusters/{clusterName}` (returns `map[string]interface{}`, an empty body is fine) |
| ListClusters | `GET /v2/projects/{projectId}/regions/{region}/clusters` |
| CreateKubeconfig | `POST .../clusters/{clusterName}/kubeconfig` |
| GetLoginKubeconfig, GetIDPKubeconfig | `GET .../kubeconfig/login`, `.../kubeconfig/idp` |
| ListProviderOptions | `GET /v2/regions/{region}/provider-options` (no project in path) |
| Credentials rotation, hibernate, wakeup, reconcile, maintenance | `POST .../clusters/{name}/{start-credentials-rotation, complete-credentials-rotation, hibernate, wakeup, reconcile, maintenance}` |

**Prerequisite: Service Enablement.** `Create` of a cluster first calls the Service Enablement API
(`TF/.../ske/cluster/resource.go` L1011ff, service id `cloud.stackit.ske` from `TF/.../utils/utils.go`).
This needs `service_enablement_custom_endpoint`. SDK: `services/serviceenablement/v2api`. Base:
`https://service-enablement.api.stackit.cloud`.

- `POST /v2/projects/{projectId}/regions/{region}/services/{serviceId}`: enable. No body needed (2xx).
- `GET` same path: returns `ServiceStatus`, e.g. `{"serviceId":"cloud.stackit.ske","state":"ENABLED"}`.
  **`state` must be present** (`state is missing` is an error otherwise).
- `DELETE` same path: disable.
- `ServiceStatus` fields are all optional. `state` enum: `ENABLED`, `ENABLING`, `DISABLED`, `DISABLING`.
- Wait (`serviceenablement/v2api/wait/wait.go`): sleep 15 s, then done on `ENABLED`, error on `DISABLED` or
  `DISABLING`, `ENABLING` is in progress. A 404 aborts.
- Error body: `ErrorResponse {error, message, path, status, timestamp}`, all optional.

Provider create flow:

1. Enable the service and wait.
2. `GET /v2/regions/{region}/provider-options`. `kubernetesVersions` and `machineImages` **must be non-nil**, or
   the provider returns an error. The provider picks the latest version whose `state == "supported"`
   (lowercase; states `supported`, `preview`, `deprecated`). Image `name` must equal the config `os_name`.
3. `PUT` the cluster, then poll `GET` the cluster.

Models (`ske/v2api/model_*.go`):

- `ProviderOptions` (all optional):
  - `kubernetesVersions[{version, state, expirationDate, featureGates}]`.
  - `machineImages[{name, versions[{version, state, expirationDate, cri[{name}]}]}]`.
  - `machineTypes[{name, cpu, memory, gpu, architecture}]`, `availabilityZones[{name}]`, `volumeTypes[{name}]`.
- `CreateOrUpdateClusterPayload` (request) and `Cluster` (response) share these fields:
  - Required: `kubernetes*: {version*}`, `nodepools*`.
  - Nodepool required fields: `name*`, `machine*: {type*, image*:{name*,version*}}`, `minimum*`, `maximum*`,
    `volume*: {size*, type}`, `availabilityZones*: []`.
  - Optional: `network`, `maintenance`, `hibernation`, `extensions`, `audit`, `access`, `labels`, `status`.
  - `Cluster` also has `name` and `status`.
- Simplest mock strategy: store the PUT payload, echo it back on GET, set `name`, and add `status`.
  The provider reads `kubernetes.version`, nodepools (including `machine.image.name` and `.version`), `network`,
  `maintenance`, `hibernation`, `extensions`, `access` and `status.{egressAddressRanges, podAddressRanges, serviceAccountIssuer}`.
  (`ske/cluster/resource.go` `mapFields`.)
- `ClusterStatus`: `aggregated` (enum below), `creationTime`, `credentialsRotation {phase, lastInitiationTime,
  lastCompletionTime}`, `error {code, message, details}`, `egressAddressRanges[]`, `podAddressRanges[]`,
  `nodeAddressRanges[]`, `serviceAddressRanges[]`, `serviceAccountIssuer`, `hibernated`, `identity`.
- `ClusterStatusState`: `STATE_HEALTHY`, `STATE_CREATING`, `STATE_DELETING`, `STATE_UNHEALTHY`, `STATE_RECONCILING`,
  `STATE_HIBERNATED`, `STATE_HIBERNATING`, `STATE_WAKINGUP`.
- Credentials rotation phases: `NEVER`, `PREPARING`, `PREPARED`, `COMPLETING`, `COMPLETED`.
- `ListClustersResponse`: `{"items": [Cluster]}`. Each item must have `name`. Return `{"items":[]}` when empty.
- `CreateKubeconfigPayload` (request): `expirationSeconds` (a **string**).
  `Kubeconfig` (response): `{"kubeconfig":"<yaml>","expirationTimestamp":"RFC3339"}`.
  **[UNCERTAIN]** which of the two the provider maps into state (`kubeconfig/resource.go` `createKubeconfig`); return both.

Wait handlers (`ske/v2api/wait/wait.go`):

- Create/Update:
  - GET cluster. **`status.aggregated` is dereferenced without a nil check, so omitting it panics the provider.**
  - Done on `STATE_HEALTHY` or `STATE_HIBERNATED`.
  - Also done on `STATE_UNHEALTHY` with `status.error.code == SKE_OBSERVABILITY_INSTANCE_NOT_FOUND` and a message.
  - There is **no error state**. `STATE_UNHEALTHY` without that code just waits until the 45 min timeout.
  - Any GET error aborts.
- Delete:
  - Polls **`ListClusters`**, not GET.
  - Done once no item has `name == clusterName`. Timeout 90 min.
  - A DELETE on a missing cluster returning 404 is tolerated by the provider (resource removed from state).
- Hibernate and Wakeup finish when the state is no longer `STATE_HIBERNATING` or `STATE_WAKINGUP`.
  Reconcile and Maintenance finish when the state is no longer `STATE_RECONCILING`.
- Credentials rotation: start is done on phase `PREPARED` (errors on `NEVER`, `COMPLETING`, `COMPLETED`).
  Complete is done on phase `COMPLETED`. Requires `status.credentialsRotation.phase`.

Provider Read: 404 on GET cluster removes it from state.

Error body: the SDK decodes 400, 401 and 404 into `map[string]interface{}`. Any JSON object works, for example
`{"code":404,"message":"..."}`. Non-JSON bodies still produce an error with the status code.
**[UNCERTAIN]** the exact real error shape.

---

## 5. PostgreSQL Flex (`postgresflex/v3api`)

SDK: `services/postgresflex/v3api/api_default.go`. Base: `https://postgres-flex-service.api.stackit.cloud`.
Provider: `TF/stackit/internal/services/postgresflex/{instance,user,database,flavors}`.
Let `P = /v3/projects/{projectId}/regions/{region}`.

| Op | Method and path |
|---|---|
| CreateInstance | `POST P/instances` |
| GetInstance | `GET P/instances/{instanceId}` |
| PartialUpdateInstance | `PATCH P/instances/{instanceId}` |
| UpdateInstance | `PUT P/instances/{instanceId}` |
| DeleteInstance | `DELETE P/instances/{instanceId}` |
| ListInstances | `GET P/instances` |
| CreateUser | `POST P/instances/{id}/users` |
| GetUser | `GET P/instances/{id}/users/{userId}` (`userId` is int64) |
| PartialUpdateUser | `PATCH .../users/{userId}` |
| DeleteUser | `DELETE .../users/{userId}` |
| ListUsers | `GET .../users` |
| ResetUserPassword | `POST .../users/{userId}/reset` |
| CreateDatabase | `POST P/instances/{id}/databases` |
| GetDatabase | `GET .../databases/{databaseId}` (int64) |
| PartialUpdateDatabase | `PATCH .../databases/{databaseId}` |
| DeleteDatabase | `DELETE .../databases/{databaseId}` |
| ListFlavors | `GET P/flavors` (`size`, `page` query) |
| ListVersions | `GET P/versions` |

The provider's only list call is `ListFlavors`, used to translate `flavor {cpu, ram}` plus `replicas` into a
`flavorId` (`instance/resource.go` `loadFlavorId`). `ListVersions` is not used by the resources.

JSON (`*` = required):

- `CreateInstancePayload` (request): `name*`, `version*`, `flavorId*`, `backupSchedule*`, `retentionDays*`,
  `network* {accessScope, acl*: []}`, `storage* {class*, size*}`, `labels`, `encryption`.
- `CreateInstanceResponse`: `{"id*": "<uuid>"}`.
- `GetInstanceResponse`:
  - `id*`, `name*`, `state*`, `version*`, `flavorId*`, `backupSchedule*`, `retentionDays*`, `isDeletable*`,
    `network* {acl*, accessScope, instanceAddress, routerAddress}`, `storage* {class, size}`,
    `connectionInfo* {write* {host*, port*}}`, `labels`, `encryption`.
- `State` enum: `READY`, `PENDING`, `PROGRESSING`, `FAILURE`, `UNKNOWN`, `TERMINATING`.
- `ListInstancesResponse`: `{instances*: [{id*,name*,state*,isDeletable*}], pagination*}`.
- `Pagination`: `{page*,size*,sort*,totalPages*,totalRows*}`. All are required in every list response.
- Flavors:
  - `ListFlavorsResponse {flavors*: [...], pagination*}`.
  - Item: `{id*, cpu*, memory*, description*, nodeType*, minGB*, maxGB*, storageClasses*: [{class*, maxIoPerSec*, maxThroughInMb*}]}`.
  - `nodeType` must be **`"Single"` or `"Replica"`** (provider constants), mapped from `replicas` 1 and 3.
  - `memory` is the provider's `ram` in GB.
  - Pagination loop: the provider calls with `size=100`, then pages while `page < totalPages`.
    Return `totalPages: 1` to stop.
- `CreateUserPayload` (request): `name*`, `roles`.
- `CreateUserResponse`: `{id* (int64), name*, password*, state*}`.
- `GetUserResponse`: `{id*, name*, roles*, state*}`. `state` is a plain string.
- `ListUsersResponse`: `{users*:[{id*,name*,state*}], pagination*}`.
- `CreateDatabasePayload`: `name*`, `owner`. `CreateDatabaseResponse`: `{id*}` (int64).
- `GetDatabaseResponse`: `{id*, name*, owner*}`.
- `ListVersionsResponse`: `{versions*: [{version*, beta*, deprecated*, recommend*}]}`.

Wait handlers (`postgresflex/v3api/wait/wait.go`):

- **Create instance**:
  - Sleep 15 s, then GET until `state == READY`.
  - `FAILURE`, `UNKNOWN` and `TERMINATING` are errors.
  - `PENDING` and `PROGRESSING` are in progress. An empty state is an error. Timeout 45 min.
- **Partial update / clone**: same, no sleep.
- **Delete instance**:
  - Done when GET returns **404 only** (`DeleteHttpErrorStatusCodes = [404]`).
  - `FAILURE` is an error. Timeout 15 min.
- **Create user**: GET user until `state == "PROCESSED"`. Anything else means in progress. There are no error states.
- **Delete user**: GET user must return **404** (any other error aborts). Timeout 1 min.
- The database resource has no wait handler. The provider calls `CreateDatabase` (retrying on HTTP **423**,
  5 attempts), then `GetDatabase` with the returned id.
- Provider read paths treat **404** as gone.

Error bodies (`model_error.go`): `{"code":404,"message":"...","traceId":"...","type":"..."}`. 400, 401, 403, 404, 409,
422, 500, 501 and 503 are decoded into it.
`ValidationError` (422) has `code*` and `validation*[]`.

---

## 6. MongoDB Flex (`mongodbflex/v2api`)

SDK: `services/mongodbflex/v2api/api_default.go`. Base: `https://mongodb-flex-service.api.stackit.cloud`.
Provider: `TF/stackit/internal/services/mongodbflex/{instance,user}`.
Let `M = /v2/projects/{projectId}/regions/{region}`.

There is **no database endpoint** in MongoDB Flex v2. Users carry a `database` field.

| Op | Method and path |
|---|---|
| CreateInstance | `POST M/instances` |
| GetInstance | `GET M/instances/{instanceId}` |
| PartialUpdateInstance | `PATCH M/instances/{instanceId}` |
| UpdateInstance | `PUT M/instances/{instanceId}` |
| DeleteInstance | `DELETE M/instances/{instanceId}` |
| ListInstances | `GET M/instances` |
| **UpdateBackupSchedule** | `PUT M/instances/{instanceId}/backups` (called on create and update) |
| CreateUser | `POST M/instances/{id}/users` |
| GetUser | `GET .../users/{userId}` (string id) |
| UpdateUser | `PUT .../users/{userId}` |
| PartialUpdateUser | `PATCH .../users/{userId}` |
| DeleteUser | `DELETE .../users/{userId}` |
| ListUsers | `GET .../users` |
| ResetUser | `POST .../users/{userId}/reset` |
| ListFlavors | `GET M/flavors` |
| ListVersions | `GET M/versions` |
| ListStorages | `GET M/storages/{flavor}` |

Provider calls used: `CreateInstance`, `GetInstance`, `PartialUpdateInstance`, `DeleteInstance`, `UpdateBackupSchedule`,
`ListFlavors`, `CreateUser`, `GetUser`, `UpdateUser`, `DeleteUser`. `ListVersions` and `ListStorages` are not used.

JSON (`*` = required; **most response fields are optional pointers but the provider dereferences some**):

- `CreateInstancePayload` (request): `name*`, `version*`, `flavorId*`, `replicas*`, `backupSchedule*`,
  `acl* {items: []}`, `storage* {class, size}`, `options*` (map string to string), `labels`.
- `CreateInstanceResponse`: `{"id": "<uuid>"}`. The provider fails if `id` is nil.
- `InstanceResponse`: `{"item": Instance}`.
- `Instance`: `id`, `name`, `status`, `version`, `replicas`, `backupSchedule`, `acl {items[]}`,
  `flavor {id, cpu, memory, description}`, `storage {class, size}`, `options` (map).
- **`options` gotcha.** If `options` is present in a GET response, the provider
  `ParseInt`s the keys `snapshotRetentionDays`, `dailySnapshotRetentionDays`, `weeklySnapshotRetentionWeeks`,
  `monthlySnapshotRetentionMonths` and `pointInTimeWindowHours` (values are numeric **strings**), and reads
  `type`. Missing or empty values make the mapping fail (`mongodbflex/instance/resource.go` L824ff).
  If `options` is nil, the state gets nulls, which causes a plan diff. So always return all six keys.
- `UpdateBackupSchedulePayload` (request) and `BackupSchedule` (response): `backupSchedule`,
  `snapshotRetentionDays`, `dailySnapshotRetentionDays`, `weeklySnapshotRetentionWeeks`,
  `monthlySnapshotRetentionMonths`, `pointInTimeWindowHours` (int32). **The provider dereferences all five numeric
  fields from the response without a nil check** (`mapOptions`), so the mock must return them.
- `InstanceStatus` enum: `READY`, `PENDING`, `PROCESSING`, `FAILED`, `UNKNOWN`.
- `ListFlavorsResponse`: `{"flavors": [{id, cpu, memory, description, categories[]}]}`. The provider matches
  `cpu` and `memory` (its `ram`) and needs `id`, `cpu` and `memory` non-nil.
- `ListInstancesResponse`: `{"items":[{id,name,status}]}`.
- `CreateUserPayload` (request): `database*`, `roles*`, `username`.
- `CreateUserResponse`: `{"item": {id, username, database, roles[], host, port, password, uri}}`.
  The provider requires **`id`** and **`password`** in the create response.
- `GetUserResponse`: `{"item": {id, username, database, roles[], host, port}}` (no password).
- `UpdateUserPayload`: `database*`, `roles*`.

Wait handlers (`mongodbflex/v2api/wait/wait.go`):

- **Create**: sleep 5 s, then GET until `item.status == READY`. `FAILED` is an error. `PENDING`, `PROCESSING` and
  `UNKNOWN` are in progress. `item`, `item.id` and `item.status` must be non-nil. Timeout 45 min.
- **Update / PartialUpdate**: same without the sleep.
- **Delete**: GET until it fails with 403, 404 or 410 (the defaults). `FAILED` is an error. Timeout 15 min.
- Provider read treats **404** as gone for both instance and user.

Error body (`model_error.go`): `{"code":404,"message":"...","type":"...","fields":{"field":["msg"]}}` (all optional).

---

## 7. Mock design hints, grouped by gotcha

1. Always send `Content-Type: application/json` on any response with a body.
2. Return the full required-field set (section 0, point 3). When in doubt, echo the stored request and add the
   server-generated fields.
3. Make resources GETtable immediately after create, in the **final** state (`CREATED`, `AVAILABLE`, `ACTIVE`,
   `READY`, `ENABLED`, `STATE_HEALTHY`, `PROCESSED`, project `ACTIVE`).
4. After DELETE:
   - Return **404** on GET. That satisfies all delete waiters, except that Postgres instance and user need exactly 404.
   - SKE delete requires the cluster to disappear from `ListClusters`.
   - Network-area-region delete also accepts 400.
5. Emit `X-Request-Id` on volume attach and detach, and implement `GET R/requests/{id}`.
6. Serve the image upload URL (`PUT`, answer 200).
7. Implement the Service Enablement endpoints for SKE.
8. Postgres Flex: int64 user and database ids, flavors with `nodeType` `Single` or `Replica`, `pagination`
   objects everywhere. Mongo Flex: string ids, `item`/`items` wrappers, numeric-string `options`.

---

## 8. Error body summary

| Service | Shape |
|---|---|
| Resource Manager | `{error, message, path, status, timeStamp}` |
| Service Account | `{error, message, path, status, timeStamp}`, auth errors `{error:{code,message,status}}` |
| IaaS | `{code, msg}` |
| SKE | any JSON object |
| Service Enablement | `{error, message, path, status, timestamp}` (optional) |
| Postgres Flex | `{code, message, traceId, type}` |
| MongoDB Flex | `{code, message, type, fields}` |

`GenericOpenAPIError.Error()` prints `<status text>, status code N, Body: <raw body>`. The provider and wait
handlers branch on `StatusCode` only. The raw body is used just for the network-area-region check
(`region not enabled`).

---

## 9. Authentication: token and key flow

SDK: `core/auth/auth.go`, `core/clients/{token_flow,key_flow,auth_flow}.go`.

Provider attributes (`TF/stackit/provider.go` L195ff and L590ff): `service_account_token`, `service_account_key`,
`service_account_key_path`, `private_key`, `private_key_path`, `token_custom_endpoint`.

`SetupAuth` precedence: workload identity federation, then service account key or key path, then token, then the
default chain (env or `~/.stackit/credentials.json`).

### Token flow (recommended for the mock)

- `service_account_token = "anything"`. No network call and no JWT validation. Every request gets
  `Authorization: Bearer anything`. `TokenFlow.validate` only rejects an empty string.
- Env alternative: `STACKIT_SERVICE_ACCOUNT_TOKEN`.

### Key flow (if you want to exercise it)

1. `service_account_key` (JSON string) must unmarshal into `ServiceAccountKeyResponse`
   (`key_flow.go`):
   - `id` (UUID), `active`, `createdAt`, `keyAlgorithm`, `keyOrigin`, `keyType`, `publicKey`.
   - `credentials {aud, iss, kid, sub (UUID), privateKey, tokenEndpoint}`.
2. A private key (PEM, RSA, PKCS1 or PKCS8 via `jwt.ParseRSAPrivateKeyFromPEM`) must be available via `private_key`
   or `credentials.privateKey`.
3. Token URL precedence: `token_custom_endpoint` (provider), then env `STACKIT_TOKEN_BASEURL`, then
   `credentials.tokenEndpoint`, then `https://service-account.api.stackit.cloud/token`.
4. Request (`key_flow.go` `requestToken`):

   ```
   POST <tokenUrl>
   Content-Type: application/x-www-form-urlencoded
   grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion=<JWT>
   ```

   The JWT is signed RS512 with header `kid`. Claims: `iss`, `sub`, `jti`, `aud`, `iat`, `exp` (+1 h).
5. Response must be **HTTP 200** (`parseTokenResponse`; anything else is an error carrying the body) with JSON:

   ```json
   {"access_token":"<JWT>","expires_in":3600,"refresh_token":"","scope":"","token_type":"Bearer"}
   ```

   The SDK model is `TokenResponseBody` (`auth_flow.go`). `access_token` **must be a parseable JWT with an `exp`
   claim** (`tokenExpired` uses `ParseUnverified` and `GetExpirationTime`). An opaque string causes
   `parse token` errors. `exp` must be more than 5 s in the future (`defaultTokenExpirationLeeway`), or the SDK
   re-requests the token on every API call. The signature is not verified by the client.
6. API calls then carry `Authorization: Bearer <access_token>`.
7. A refresh token is not required. If `refresh_token` is an empty string, `tokenExpired("")` is true and the SDK
   falls back to the JWT-bearer grant again.

The Service Account SDK also models the same endpoint (`POST /token`, `CreateShortLivedAccessToken`,
`CreateShortLivedAccessTokenResponse {access_token*, expires_in*, refresh_token*, scope*, token_type*}`).
The core client's `KeyFlow` is what the provider uses.

---

## 10. Provider configuration for custom endpoints

Defined in `TF/stackit/provider.go` (schema L389ff, mapping L590ff). Each attribute is a plain optional string.
There is **no validator**, and nothing in the provider or SDK enforces `https`.

- `config.WithEndpoint(url)` (`SDK/core/config/config.go` L158) replaces the server list with exactly that URL.
- Every call is `localBasePath + "/v2/..."`, so `http://` is accepted. A path prefix is allowed, for example
  `http://localhost:8080/iaas`.
- Setting a custom endpoint also disables the region-in-hostname logic (`ConfigureRegion` returns early when
  `setCustomEndpoint` is true). The region stays a path parameter only.
- Region resolution: `default_region` (provider attribute) or the per-resource `region`. Set
  `default_region = "eu01"` to be explicit.

Attributes for the services in this document:

| Service | Provider attribute |
|---|---|
| Resource Manager | `resourcemanager_custom_endpoint` |
| Service Account | `service_account_custom_endpoint` |
| IaaS | `iaas_custom_endpoint` |
| SKE | `ske_custom_endpoint` |
| Service Enablement (SKE prerequisite) | `service_enablement_custom_endpoint` |
| PostgreSQL Flex | `postgresflex_custom_endpoint` |
| MongoDB Flex | `mongodbflex_custom_endpoint` |
| Token endpoint (key flow only) | `token_custom_endpoint` |
| Membership / Authorization | `authorization_custom_endpoint` (not called by the project resource code read here; **[UNCERTAIN]** whether other resources need it) |

All clients are created eagerly at provider configure time (`initClientCollection`, `clientutils.go` L106ff), but
no network call happens until a resource or data source is used.

### Minimal provider block

Use a path prefix per service. It avoids the `GET /v2/projects/{id}` collision between Resource Manager and IaaS:

```hcl
provider "stackit" {
  default_region        = "eu01"
  service_account_token = "mock-token"   # token flow: no token endpoint needed

  resourcemanager_custom_endpoint    = "http://localhost:8080/resourcemanager"
  service_account_custom_endpoint    = "http://localhost:8080/serviceaccount"
  iaas_custom_endpoint               = "http://localhost:8080/iaas"
  ske_custom_endpoint                = "http://localhost:8080/ske"
  service_enablement_custom_endpoint = "http://localhost:8080/serviceenablement"
  postgresflex_custom_endpoint       = "http://localhost:8080/postgresflex"
  mongodbflex_custom_endpoint        = "http://localhost:8080/mongodbflex"
}
```

Without prefixes (every service at the root `http://localhost:8080`), only the single path
`GET /v2/projects/{id}` is ambiguous (Resource Manager `GetProject` vs. IaaS `GetProjectDetails`). IaaS
`GetProjectDetails` is used only by the `stackit_iaas_project` data source, so the root layout works if that data
source is not needed.

Key-flow variant: replace `service_account_token` with `service_account_key = file("sa-key.json")`,
`private_key = file("key.pem")` and `token_custom_endpoint = "http://localhost:8080/token"`.

Environment fallbacks: `STACKIT_SERVICE_ACCOUNT_TOKEN`, `STACKIT_SERVICE_ACCOUNT_KEY(_PATH)`,
`STACKIT_PRIVATE_KEY(_PATH)`, `STACKIT_TOKEN_BASEURL`, `STACKIT_CREDENTIALS_PATH` (`SDK/core/auth/auth.go`,
`key_flow.go`).

---

## 11. Not verified or out of scope

- Real success status codes (see section 0).
- Real error body shapes for SKE and for non-4xx codes.
- IaaS beta resources (`iaasalpha`: VPC, VPC regions and similar, `v2alpha1api`), network-area routes and routing
  tables, and affinity groups, backups, snapshots and the other services in the provider (DNS, load balancer,
  object storage and so on) are not covered.
- Mongo/Postgres backup, restore, clone and metrics endpoints are not covered.
- The `terraform-example` directory in `docs/` is untouched.
