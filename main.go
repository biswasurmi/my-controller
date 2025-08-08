package main

import (
    "context"
    "flag"
    "os"
    "os/signal"
    "syscall"
    "time"

    "k8s.io/apimachinery/pkg/util/wait"
    "k8s.io/client-go/informers"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/clientcmd"
    "k8s.io/klog/v2"

    clientset "github.com/biswasurmi/my-controller/pkg/generated/clientset/versioned"
    sampleinformers "github.com/biswasurmi/my-controller/pkg/generated/informers/externalversions"
    "github.com/biswasurmi/my-controller/pkg/controller"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
    var kubeconfig string
    var masterURL string

    klog.InitFlags(nil)
    flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Required if running out-of-cluster.")
    flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides kubeconfig.")
    flag.Parse()

    // Build Kubernetes config
    cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
    if err != nil {
        klog.Fatalf("Error building kubeconfig: %s", err)
    }

    // Create Kubernetes clientset
    kubeClient, err := kubernetes.NewForConfig(cfg)
    if err != nil {
        klog.Fatalf("Error building Kubernetes clientset: %s", err)
    }

    // Create MyApp clientset
    myappClient, err := clientset.NewForConfig(cfg)
    if err != nil {
        klog.Fatalf("Error building MyApp clientset: %s", err)
    }

    // Create informer factories
    kubeInformerFactory := informers.NewSharedInformerFactory(kubeClient, 30*time.Second)
    myappInformerFactory := sampleinformers.NewSharedInformerFactory(myappClient, 30*time.Second)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Debug: list MyApps directly using clientset (to verify client works)
    myapps, err := myappClient.UrmiV1alpha1().MyApps("default").List(context.TODO(), metav1.ListOptions{})
    if err != nil {
        klog.Errorf("Error listing MyApps: %v", err)
    } else {
        for _, app := range myapps.Items {
            klog.Infof("Direct list MyApp found: %s/%s", app.Namespace, app.Name)
        }
    }

    // Create controller
    ctrl := controller.NewController(
        ctx,
        kubeClient,
        myappClient,
        kubeInformerFactory.Apps().V1().Deployments(),
        myappInformerFactory.Urmi().V1alpha1().MyApps(),
    )

    // Setup signal handler for graceful shutdown
    stopCh := make(chan os.Signal, 1)
    signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)

    // Start informer factories
    kubeInformerFactory.Start(ctx.Done())
    myappInformerFactory.Start(ctx.Done())

    // Run controller
    go func() {
        if err := ctrl.Run(ctx, 2); err != nil {
            klog.Fatalf("Error running controller: %s", err)
        }
    }()

    // Wait for shutdown signal
    <-stopCh
    klog.Info("Shutdown signal received, exiting...")
    cancel()
    // Optional: wait some seconds for cleanup (not strictly needed)
    wait.PollImmediate(time.Second, 5*time.Second, func() (bool, error) { return true, nil })
}
