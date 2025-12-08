# How-to Import New or Upgrade Existing Helm Charts

1. Retrieve upstream helm-chart. We retrieve charts this way since many are embedded in a subdirectory called charts for the applications themselves, this is easier.
   ```
   helm repo add kubernetes-dashboard https://kubernetes.github.io/dashboard/
   helm repo update
   helm pull kubernetes-dashboard/kubernetes-dashboard --version 7.4.0 --untar
   ```
2. Discard `./charts` subdirectory as well as `Chart.lock` file
   ```
   rm -r Chart.lock charts
   ```
3. If this is a new chart, create a new repository within the `kaas-helm-charts` org
4. If this is a chart being upgraded, clone the repo locally
5. Move or copy the new files to your local repo and create a new branch
   ```
   cp -rp ../kubernetes-dashboard/* ~/bbgithub/kaas-helm-charts/kubernetes-dashboard/
   cd ~/bbgithub/kaas-helm-charts/kubernetes-dashboard/
   git checkout -b 'update-to-7.4.0'
   ```
6. For new charts, see "General Required Changes" below. For existing charts being upgraded, continue to the next step.
7. Check `git status` and or `git diff`, there may be few to many changes from the update.
   ```
   ➜  kubernetes-dashboard git:(update-to-7.4.0) ✗ git status
   On branch main
   Your branch is up to date with 'origin/main'.

   Changes not staged for commit:
   (use "git add/rm <file>..." to update what will be committed)
   (use "git restore <file>..." to discard changes in working directory)
       modified:   Chart.yaml
       modified:   README.md
       modified:   templates/NOTES.txt
       modified:   templates/_helpers.tpl
       modified:   values.yaml

   Untracked files:
   (use "git add <file>..." to include in what will be committed)
       templates/config/
       templates/deployments/
       templates/extras/
       templates/networking/
       templates/rbac/
       templates/secrets/
       templates/security/
       templates/services/
   ```
8. Aside from the changes noted in "General Required Changes", some charts may have additional modification specific to only them. Some files will move or be removed entirely others will only have minor changes. You will have to `git diff <file>` each file and attempt to identify modifications. See "Chart Specific Changes" for any already documented known changes. 
9. Verify the chart will fresh install and upgrade successfully, you may need to update the `values.yaml` or include an additional `-f myValues.yaml` to work for your specific cluster.
   ```
   helm upgrade --install -n kube-system kubernetes-dashboard ./
   helm uninstall -n kube-system kubernetes-dashboard
   helm upgrade --install -n kube-system kubernetes-dashboard ./
   ```
   1.  If the chart is already installed on your cluster, you can retrieve the current values by running.

       NOTE: Note the format of the secret name is sh.helm.release.v1.<release-name>.<version> where version is something like v1, v2, etc depending on the number of upgrades done.
      ```
      kubectl get secrets -n sot sh.helm.release.v1.kine-apiserver.v2 -o jsonpath="{.data.release}" | base64 -d | base64 -d | gzip -d | jq .config | yq -p json -o yaml > myValues.yaml
      ```
10. Check `Charts.yaml` for any new or updated required dependencies. These steps will need to be performed for each of the charts.
    ```
    ➜  kubernetes-dashboard git:(update-to-7.4.0) ✗ cat Chart.yaml
    ...snip...
    dependencies:
    - alias: nginx
    condition: nginx.enabled
    name: ingress-nginx
    repository: https://kubernetes.github.io/ingress-nginx
    version: 4.10.0
    - condition: cert-manager.enabled
    name: cert-manager
    repository: https://charts.jetstack.io
    version: v1.14.3
    - condition: metrics-server.enabled
    name: metrics-server
    repository: https://kubernetes-sigs.github.io/metrics-server/
    version: 3.12.0
    - condition: kong.enabled
    name: kong
    repository: https://charts.konghq.com
    version: 2.38.0
    ...snip...
    ```


## General Required Changes
Whether the changes are made in `values.yaml`, a deployment, daemonset, or stateful manifests. Populate the following configurations:
- Update Image Repository: `artifactory.inf.bloomberg.com/kubernetesinfrastructure/ext`
  ```
  +    repository: artifactory.inf.bloomberg.com/kubernetesinfrastructure/ext/docker.io/kubernetesui/dashboard-auth
  -    repository: docker.io/kubernetesui/dashboard-auth
  ...snip...
  +    repository: artifactory.inf.bloomberg.com/kubernetesinfrastructure/ext/docker.io/kubernetesui/dashboard-api
  -    repository: docker.io/kubernetesui/dashboard-api
  ...snip...
  +    repository: artifactory.inf.bloomberg.com/kubernetesinfrastructure/ext/docker.io/kubernetesui/dashboard-web
  -    repository: docker.io/kubernetesui/dashboard-web
  ...snip...
  +    repository: artifactory.inf.bloomberg.com/kubernetesinfrastructure/ext/docker.io/kubernetesui/dashboard-metrics-scraper
  -    repository: docker.io/kubernetesui/dashboard-metrics-scraper
  ```

- Update Helm Chart Repository for dependencies in `Charts.yaml`
  ```
    +  repository: https://artifactory.inf.bloomberg.com/artifactory/bloomberg-helm-charts
    -  repository: https://kubernetes.github.io/ingress-nginx

    +  repository: https://artifactory.inf.bloomberg.com/artifactory/bloomberg-helm-charts
    -  repository: https://charts.jetstack.io

    +  repository: https://artifactory.inf.bloomberg.com/artifactory/bloomberg-helm-charts
    -  repository: https://kubernetes-sigs.github.io/metrics-server/

    +  repository: https://artifactory.inf.bloomberg.com/artifactory/bloomberg-helm-charts
    -  repository: https://charts.konghq.com
  ```
  
- Configure Tolerations
  ```
  tolerations:
    - key: addons.kaas.bloomberg.com/unavailable
      operator: "Exists"
      effect: NoSchedule
  ```
  - If this is a critical daemonset, we will have to tolerate all taints with:
    ```
    tolerations:
      - operator: "Exists"
        effect: NoSchedule
    ```
  - Similarly, if this is a control-plane deployment:
    ```
    tolerations:
      - key: node-role.kubernetes.io/control-plane
        operator: "Exists"
        effect: NoSchedule
    ```
    Also, check the affinity section for additional configurations regarding control-plane deployments.

    See: https://kubernetes.io/docs/reference/labels-annotations-taints/#node-role-kubernetes-io-control-plane-taint

- Configure Affinity
  - This affinity configuration is required for all charts:
  ```
  affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: kubernetes.io/role
              operator: NotIn
              values:
                - monitor
  ```
  - If this is a control plane deployment, add the following affinity configuration:
    ```
    affinity:
    nodeAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
        - weight: 100
          preference:
            matchExpressions:
              - key: node-role.kubernetes.io/control-plane
                operator: Exists
    ```

- Configure Priority Class
  - If this is a critical chart required for cluster functionality:
  ```
  priorityClassName: "system-cluster-critical"
  ```
  
  - If this is a critical chart required for node functionality:
  ```
  priorityClassName: "system-node-critical"
  ```
  - If this is not a critical chart:
  ```
  priorityClassName: ""
  ```
  
- Configure envFrom
  ```
  envFrom:
    - configMapRef:
        # Allow KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT to be overridden
        name: kubernetes-services-endpoint
        optional: true
  ```

## Uploading Images
Use the following script to scan and upload images:
- Required dependencies:
  ```
  brew install cosign
  brew install trivy
  ```

  ```
  #!/bin/bash

  set -euxo pipefail

  export KAAS_USERNAME='kubernetes'
  export KAAS_PASSWORD='<BPV => KaaS => artifactory>'

  export http_proxy=http://proxy.bloomberg.com:81
  export https_proxy=http://proxy.bloomberg.com:81
  export no_proxy=artprod.dev.bloomberg.com

  cosign login artprod.dev.bloomberg.com -u "${KAAS_USERNAME}" -p "${KAAS_PASSWORD}"

  IMAGES=(
      docker.io/kubernetesui/dashboard-auth:1.1.3
      docker.io/kubernetesui/dashboard-api:1.6.0
      docker.io/kubernetesui/dashboard-web:1.3.0
      docker.io/kubernetesui/dashboard-metrics-scraper:1.1.1
  )

  ARTIFACTORY_PREFIX="artprod.dev.bloomberg.com/kubernetesinfrastructure/ext"

  for image in ${IMAGES[@]}; do
      echo $image
      trivy image "${image}"
      #cosign copy -f "$image" "$ARTIFACTORY_PREFIX/$image"
  done
  ```

- If you notice critical CVEs after the scan. See if there is an updated chart you can use.

## Chart Specific Changes

### Kubernetes-Dashboard
- Adding BSSO config to `values.yaml`:
```
{{- if .Values.bssoIDXProxy.enabled }}
        - name: bsso-idx-proxy
          image: "{{ .Values.bssoIDXProxy.image.repository }}:{{ .Values.bssoIDXProxy.image.tag }}"
          imagePullPolicy: {{ .Values.bssoIDXProxy.image.pullPolicy }}
          env:
            - name: PORT
              value: "{{ .Values.bssoIDXProxy.port }}"
            - name: DEBUG
              value: "1"
            - name: BSSO_USE_PROD
              value: "{{ .Values.bssoIDXProxy.bssoUseProd }}"
            - name: BSSO_CLIENT_ID
              value: "{{ .Values.bssoIDXProxy.bssoClientID }}"
            - name: BSSO_CLIENT_SECRET
              valueFrom:
                secretKeyRef:
                  name: "{{ .Values.bssoIDXProxy.bssoClientSecret.name }}"
                  key: "{{ .Values.bssoIDXProxy.bssoClientSecret.key }}"
            - name: IDX_INPUT_TOKEN_TYPE
              value: "{{ .Values.bssoIDXProxy.idxInputTokenType }}"
            - name: IDX_OUTPUT_TOKEN_TYPE
              value: "{{ .Values.bssoIDXProxy.idxOutputTokenType }}"
            - name: IDX_ACTOR_TOKEN_TYPE
              value: "{{ .Values.bssoIDXProxy.idxActorTokenType }}"
            - name: IDX_ACTOR_TOKEN
              valueFrom:
                secretKeyRef:
                  name: "{{ .Values.bssoIDXProxy.idxActorTokenSecret.name }}"
                  key: "{{ .Values.bssoIDXProxy.idxActorTokenSecret.key }}"
            - name: IDX_URL
              value: "{{ .Values.bssoIDXProxy.idxURL }}"
            - name: REDIRECT_URL
              value: "{{ .Values.bssoIDXProxy.bssoRedirectURL }}"
            - name: PROXY_TARGET_URL
              value: "http://localhost:8000"
            - name: PROXY_TARGET_CA_PATH
              value: ""
          ports:
            - name: {{ .Values.bssoIDXProxy.role }}
              containerPort: {{ .Values.bssoIDXProxy.port }}
              protocol: TCP
          livenessProbe:
            httpGet:
              scheme: HTTP
              path: /healthz
              port: {{ .Values.bssoIDXProxy.port }}
            initialDelaySeconds: 1
            timeoutSeconds: 3
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
              - ALL
            readOnlyRootFilesystem: true
{{- end }}
```

- Adding BSSO conditional to `services/web.yaml`
  ```
    @@ -25,12 +25,7 @@ metadata:
    name: {{ template "kubernetes-dashboard.fullname" . }}-{{ .Values.web.role }}
    spec:
    ports:
    +    {{- if .Values.bssoIDXProxy.enabled }}
    +    - name: {{ .Values.bssoIDXProxy.role }}
    +      targetPort: {{ .Values.bssoIDXProxy.role }}
    +    {{- else }}
        - name: {{ .Values.web.role }}
    +    {{- end }}
        {{- with (index .Values.web.containers.ports 0) }}
        port: {{ .containerPort }}
        {{- end }}
  ```

- Adding reloader annotation for BSSO secret
  ```
  annotations: {
    "secret.reloader.stakater.com/reload": "bsso-idx-proxy"
  }
  ```

- Enable Ingress by default:
  ```
    ingress:
    +    enabled: true
    -    enabled: false
  ```

- Update Ingress Class Name:
  ```
    +    ingressClassName: nginx
    -    ingressClassName: internal-nginx
  ```

## Publishing the helm chart to Artifactory

1. The chart should be merged into this repo https://bbgithub.dev.bloomberg.com/kaas-helm-charts/
2. Create a Jenkins file similar to https://bbgithub.dev.bloomberg.com/kaas-helm-charts/kubernetes-dashboard/blob/main/Jenkinsfile
3. Make sure artifactory path and helm versions are specified correctly:
```json
    @Library('jaazy-jeff-jaas') _

node('docker && dev') {
    def j = jaazy()

    def helmEnv = j.env("Docker")
        .setImage("artifactory.inf.bloomberg.com/kubernetesinfrastructure/helm:v3.13.1")

    def helmAgent = j.agent("kubernetes.HelmAgent")
        .inside(helmEnv)
        .setPublishCredential("kubeinfra-artifactory")
        .setPublishPrereleases(true)
        .setPublishSubroute("kaas")

    j.jaazyflow()
        .addBranchPair("develop", "main")
        .using(helmAgent, ["info", "test", "build", "publish"])
        .start()
}
```
4. Once the Jenkins file is merged, the chart will automatically publish to the Artifactory.