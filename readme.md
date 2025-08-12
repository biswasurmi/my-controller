docker build -t urmibiswas/myapp-controller:v3 .

docker push urmibiswas/my-controller:v3

kubectl get deployments -n default

kubectl get pods -n default -l app=my-book-project

kubectl port-forward -n default pod/my-book-project-deployment-6bdd6cc6d6-2qddj 9090:8080

