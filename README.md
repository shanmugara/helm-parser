# helm-parser

## Module: `update-registry.go`

### Function: `collectImagesRecursive`

**Purpose:** Recursively traverses Kubernetes manifests to extract all container images from `containers` and `initContainers` fields.

**Signature:**
```go
func collectImagesRecursive(node interface{}, images *[]string)
```

#### Example Usage

Given this Kubernetes manifest:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      initContainers:
      - name: init-db
        image: busybox:1.28
      containers:
      - name: app
        image: nginx:1.19
      - name: sidecar
        image: envoy:v1.18
```

**After `yaml.Unmarshal`, the structure becomes:**

```go
node = map[interface{}]interface{}{
    "apiVersion": "apps/v1",
    "kind": "Deployment",
    "spec": map[interface{}]interface{}{
        "template": map[interface{}]interface{}{
            "spec": map[interface{}]interface{}{
                "initContainers": []interface{}{
                    map[interface{}]interface{}{
                        "name": "init-db",
                        "image": "busybox:1.28",
                    },
                },
                "containers": []interface{}{
                    map[interface{}]interface{}{
                        "name": "app",
                        "image": "nginx:1.19",
                    },
                    map[interface{}]interface{}{
                        "name": "sidecar",
                        "image": "envoy:v1.18",
                    },
                },
            },
        },
    },
}
```

**Execution flow:**

1. **Level 1**: Traverse root map → find key `"spec"` → recurse
2. **Level 2**: Traverse spec map → find key `"template"` → recurse
3. **Level 3**: Traverse template map → find key `"spec"` → recurse
4. **Level 4**: Traverse pod spec map
   - Find key `"initContainers"` → extract images:
     - `"busybox:1.28"`
   - Find key `"containers"` → extract images:
     - `"nginx:1.19"`
     - `"envoy:v1.18"`

**Final result:**
```go
images = ["busybox:1.28", "nginx:1.19", "envoy:v1.18"]
```

**Type Switch Cases:**

The function uses a type switch with three cases to handle different YAML unmarshaling results:

1. **`case map[interface{}]interface{}`**
   - **When used:** Default case when unmarshaling with `gopkg.in/yaml.v2`
   - **Why:** `yaml.v2` creates maps with `interface{}` keys for maximum flexibility
   - **Example:** Top-level manifest keys like `"apiVersion"`, `"spec"`, `"metadata"`

2. **`case map[string]interface{}`**
   - **When used:** Alternative unmarshaling or when working with JSON-compatible structures
   - **Why:** Some YAML libraries or conversion functions produce string-keyed maps
   - **Example:** When manifest is converted from JSON or uses `encoding/json`

3. **`case []interface{}`**
   - **When used:** When encountering YAML arrays/lists
   - **Why:** Handles lists of pods, volumes, or any other array structures
   - **Example:** List of containers: `containers: [...]`, list of volumes: `volumes: [...]`

**Key characteristics:**
- **Recursive traversal**: Dives through nested maps/slices to find container definitions
- **Special key detection**: Only extracts images from keys named `"containers"` or `"initContainers"`
- **Type flexibility**: Handles both `map[interface{}]interface{}` and `map[string]interface{}`
- **Depth agnostic**: Works with any nesting level - finds containers wherever they appear
