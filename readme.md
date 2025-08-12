## Overview

**MyApp Controller** is a Kubernetes custom controller built with Go that manages `MyApp` Custom Resources (CRs) in the API group `urmi.dev/v1alpha1`. It automates deployment, scaling, and secure configuration of a book project application by:

- Creating and reconciling Kubernetes Deployments based on the `MyApp` resource spec.
- Injecting JWT secrets securely via Kubernetes Secrets.
- Allowing toggling of authentication via custom resource fields.
- Updating the CR status with live deployment replica counts.
- Managing the app declaratively without manual Deployment handling.

This controller is perfect for learning how to build Kubernetes extensions using CRDs, controllers, code generation, and best practices in secure secret management.

---

## Features

- **Custom Resource Definition (CRD)** for `MyApp` with spec fields:
  - `replicas` to scale the app.
  - `image` for the container image.
  - `auth` to enable or disable authentication.
  - `jwtSecret` referencing a Kubernetes Secret for JWT.
- **Reconciliation Loop** that ensures Deployment matches desired state.
- **Secure JWT Injection** through environment variables from Secrets.
- **Authentication Toggle** via container command args.
- **Status Updates** reporting Deployment availability in CR status.
- **Generated Clientsets, Informers, Listers** via Kubernetes code-generator.
- **Graceful Shutdown & Robust Logging** with `klog/v2`.
- **RBAC** permissions configured for controller operation.

---

## Prerequisites

- Kubernetes cluster v1.31+ (Minikube, GKE, etc.)
- `kubectl` configured for your cluster
- Go 1.24+ installed
- Docker installed and configured
- Git installed

---

## Getting Started

### 1. Clone the repository

```bash
git clone https://github.com/biswasurmi/my-controller.git
cd my-controller
````

### 2. Generate client code (if you modify CRD/types)

```bash
go install k8s.io/code-generator/cmd/...@v0.31.1
./hack/update-codegen.sh
```

### 3. Build and push the controller image

```bash
docker build -t urmibiswas/my-controller:v4 .
docker push urmibiswas/my-controller:v4
```

### 4. Deploy the CRD

```bash
kubectl apply -f config/crd/myapp_crd.yaml
```

### 5. Deploy RBAC roles and the controller

```bash
kubectl apply -f config/controller-rbac.yaml
kubectl apply -f config/controller-deployment.yaml
```

### 6. Create the JWT secret

```bash
kubectl apply -f jwt-secret.yaml
```

### 7. Deploy your MyApp resource

```bash
kubectl apply -f myapp.yaml
```

---

## Usage

* **Check Deployments and Pods**

```bash
kubectl get deployments -n default
kubectl get pods -n default -l app=my-book-project
```

* **Port-forward to access the app locally**

```bash
kubectl port-forward -n default pod/<pod-name> 9090:8080
```

Then visit: `http://localhost:9090`

* **View controller logs**

```bash
kubectl get pods -n default -l app=myapp-controller
kubectl logs -f <controller-pod-name> -n default
```

* **Update MyApp resource**

Edit `myapp.yaml` to change replicas or toggle auth, then:

```bash
kubectl apply -f myapp.yaml
```

* **Delete resources (for cleanup)**

```bash
kubectl delete deployment my-book-project-deployment -n default
kubectl delete myapp my-book-project -n default
kubectl delete -f jwt-secret.yaml
kubectl delete -f config/controller-deployment.yaml
kubectl delete -f config/controller-rbac.yaml
kubectl delete -f config/crd/myapp_crd.yaml
```

---

## Project Structure

```
├── cmd/                          # Entry point(s) for the controller app
├── config/                       # Kubernetes manifests for CRD, RBAC, and Deployment
│   ├── controller-deployment.yaml
│   ├── controller-rbac.yaml
│   └── crd/
│       └── myapp_crd.yaml
├── Dockerfile                    # Dockerfile for building the controller image
├── go.mod
├── go.sum
├── hack/                         # Scripts and tooling for code generation
│   ├── boilerplate.go.txt
│   ├── tools.go
│   └── update-codegen.sh
├── jwt-secret.yaml               # Kubernetes Secret manifest for JWT_SECRET
├── main.go                      # Main application entrypoint
├── myapp.yaml                   # Sample MyApp Custom Resource manifest
├── pkg/                         # Controller and API source code
│   ├── apis/urmi.dev/v1alpha1/ # API definitions (types.go, deepcopies, etc)
│   ├── controller/              # Controller implementation
│   └── generated/               # Generated clientsets, informers, listers
└── readme.md                    # This file
```

---


