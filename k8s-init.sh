# A script to spin up minikube clusters for local dev in playground
# Has been converted to a golang script with concurrency to portforward and so on
# Can be used from kubernetesInit/main.go

#!/usr/bin/env bash

if ! command -v minikube > /dev/null 2>&1; then
    echo "minikube not installed"
    exit 1
fi

echo "Starting Payments and Streaming Clusters..."

minikube start -p paymentsCluster --driver=docker
minikube start -p streamingCluster --driver=docker
minikube start -p sreCluster --driver=docker

minikube profile list

echo "Kubernetes clusters are up and running."

installArgocd(){
    local profile=$1
    kubectl config use-context "$profile"
    usingProfile=$(kubectl config current-context)
    echo "Installing ArgoCD in cluster: $usingProfile"
    kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -
    kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
    kubectl wait -n argocd --for=condition=available deployment/argocd-server --timeout=300s
    kuebctl port-forward svc/argo-server -n argocd 8088:443
    password=$(kubectl -n argocd get secret argocd-initial-admin-secret -p jsonpath="{.data.password}" | base64 -d)
    echo "use admin and password: $password"
}

installArgocd sreCluster


