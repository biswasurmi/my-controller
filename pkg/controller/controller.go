package controller

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/time/rate"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	v1alpha1 "github.com/biswasurmi/my-controller/pkg/apis/urmi.dev/v1alpha1"
	clientset "github.com/biswasurmi/my-controller/pkg/generated/clientset/versioned"
	samplescheme "github.com/biswasurmi/my-controller/pkg/generated/clientset/versioned/scheme"
	informers "github.com/biswasurmi/my-controller/pkg/generated/informers/externalversions/urmi.dev/v1alpha1"
	listers "github.com/biswasurmi/my-controller/pkg/generated/listers/urmi.dev/v1alpha1"
)

const controllerAgentName = "myapp-controller"

const (
	SuccessSynced         = "Synced"
	MessageResourceSynced = "MyApp synced successfully"
)

type Controller struct {
	kubeclientset  kubernetes.Interface
	myappclientset clientset.Interface

	deploymentsLister appslisters.DeploymentLister
	deploymentsSynced cache.InformerSynced

	myappsLister listers.MyAppLister
	myappsSynced cache.InformerSynced

	workqueue workqueue.TypedRateLimitingInterface[cache.ObjectName]
	recorder  record.EventRecorder
}

func NewController(
	ctx context.Context,
	kubeclientset kubernetes.Interface,
	myappclientset clientset.Interface,
	deploymentInformer appsinformers.DeploymentInformer,
	myappInformer informers.MyAppInformer,
) *Controller {
	logger := klog.FromContext(ctx)
	utilruntime.Must(samplescheme.AddToScheme(scheme.Scheme))
	logger.Info("Creating event broadcaster")

	eventBroadcaster := record.NewBroadcaster(record.WithContext(ctx))
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	ratelimiter := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[cache.ObjectName](5*time.Millisecond, 1000*time.Second),
		&workqueue.TypedBucketRateLimiter[cache.ObjectName]{Limiter: rate.NewLimiter(rate.Limit(50), 300)},
	)

	c := &Controller{
		kubeclientset:     kubeclientset,
		myappclientset:    myappclientset,
		deploymentsLister: deploymentInformer.Lister(),
		deploymentsSynced: deploymentInformer.Informer().HasSynced,
		myappsLister:      myappInformer.Lister(),
		myappsSynced:      myappInformer.Informer().HasSynced,
		workqueue:         workqueue.NewTypedRateLimitingQueue(ratelimiter),
		recorder:          recorder,
	}

	logger.Info("Setting up event handlers")

	myappInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			myapp, ok := obj.(*v1alpha1.MyApp)
			if ok {
				logger.Info("MyApp added to informer", "namespace", myapp.Namespace, "name", myapp.Name)
			}
			c.enqueueMyApp(obj)
		},
		UpdateFunc: func(old, new interface{}) {
			myapp, ok := new.(*v1alpha1.MyApp)
			if ok {
				logger.Info("MyApp updated in informer", "namespace", myapp.Namespace, "name", myapp.Name)
			}
			c.enqueueMyApp(new)
		},
		DeleteFunc: func(obj interface{}) {
			myapp, ok := obj.(*v1alpha1.MyApp)
			if ok {
				logger.Info("MyApp deleted from informer", "namespace", myapp.Namespace, "name", myapp.Name)
			}
			c.enqueueMyApp(obj)
		},
	})

	deploymentInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newDepl := new.(*appsv1.Deployment)
			oldDepl := old.(*appsv1.Deployment)
			if newDepl.ResourceVersion == oldDepl.ResourceVersion {
				return
			}
			c.handleObject(new)
		},
		DeleteFunc: c.handleObject,
	})

	return c
}

func (c *Controller) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting MyApp controller")

	if ok := cache.WaitForCacheSync(ctx.Done(), c.deploymentsSynced, c.myappsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Caches synced, starting workers", "count", workers)
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("Started workers")
	<-ctx.Done()
	logger.Info("Shutting down workers")
	return nil
}

func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	objRef, shutdown := c.workqueue.Get()
	logger := klog.FromContext(ctx)

	if shutdown {
		return false
	}

	defer c.workqueue.Done(objRef)

	err := c.syncHandler(ctx, objRef)
	if err == nil {
		c.workqueue.Forget(objRef)
		logger.Info("Successfully synced", "objectName", objRef)
		return true
	}
	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", objRef)
	c.workqueue.AddRateLimited(objRef)
	return true
}

func (c *Controller) syncHandler(ctx context.Context, objectRef cache.ObjectName) error {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "objectRef", objectRef)

	logger.Info("Starting sync for MyApp", "namespace", objectRef.Namespace, "name", objectRef.Name)

	myapp, err := c.myappsLister.MyApps(objectRef.Namespace).Get(objectRef.Name)
	if err != nil {
		if kerrors.IsNotFound(err) {
			logger.Info("MyApp not found, skipping", "objectReference", objectRef)
			return nil
		}
		logger.Error(err, "Failed to get MyApp")
		return err
	}

	logger.Info("MyApp JWT Secret", "jwtSecret", myapp.Spec.JWTSecret)

	logger.Info("Processing MyApp", "namespace", objectRef.Namespace, "name", objectRef.Name)

	deploymentName := objectRef.Name + "-deployment"
	deployment, err := c.deploymentsLister.Deployments(objectRef.Namespace).Get(deploymentName)
	desiredDeployment := newDeployment(myapp)

	logger.Info("Desired deployment container env vars", "env", desiredDeployment.Spec.Template.Spec.Containers[0].Env)

	if kerrors.IsNotFound(err) {
		logger.Info("Deployment not found, creating", "deployment", deploymentName)
		deployment, err = c.kubeclientset.AppsV1().Deployments(objectRef.Namespace).Create(ctx, desiredDeployment, metav1.CreateOptions{})
		if err != nil {
			logger.Error(err, "Failed to create Deployment", "deployment", deploymentName)
			return err
		}
		logger.Info("Created Deployment", "deployment", deploymentName)
	} else if err != nil {
		logger.Error(err, "Failed to get Deployment", "deployment", deploymentName)
		return err
	} else {
		updateNeeded := false
		deploymentCopy := deployment.DeepCopy()

		if myapp.Spec.Replicas != nil {
			if deploymentCopy.Spec.Replicas == nil || *deploymentCopy.Spec.Replicas != *myapp.Spec.Replicas {
				logger.Info("Updating Deployment replicas", "deployment", deploymentName, "desiredReplicas", *myapp.Spec.Replicas)
				deploymentCopy.Spec.Replicas = myapp.Spec.Replicas
				updateNeeded = true
			}
		}

		origContainer := deploymentCopy.Spec.Template.Spec.Containers[0]
		desiredContainer := desiredDeployment.Spec.Template.Spec.Containers[0]

		if origContainer.Image != desiredContainer.Image {
			logger.Info("Updating Deployment container image", "deployment", deploymentName, "oldImage", origContainer.Image, "newImage", desiredContainer.Image)
			deploymentCopy.Spec.Template.Spec.Containers[0].Image = desiredContainer.Image
			updateNeeded = true
		}

		if !equalStringSlices(origContainer.Command, desiredContainer.Command) {
			logger.Info("Updating Deployment container command", "deployment", deploymentName)
			deploymentCopy.Spec.Template.Spec.Containers[0].Command = desiredContainer.Command
			updateNeeded = true
		}

		if !equalStringSlices(origContainer.Args, desiredContainer.Args) {
			logger.Info("Updating Deployment container args", "deployment", deploymentName)
			deploymentCopy.Spec.Template.Spec.Containers[0].Args = desiredContainer.Args
			updateNeeded = true
		}

		origEnv := deploymentCopy.Spec.Template.Spec.Containers[0].Env
		desiredEnv := desiredDeployment.Spec.Template.Spec.Containers[0].Env

		if !equalEnvVars(origEnv, desiredEnv) {
			logger.Info("Updating Deployment container env vars", "deployment", deploymentName, "origEnv", origEnv, "desiredEnv", desiredEnv)
			deploymentCopy.Spec.Template.Spec.Containers[0].Env = desiredEnv
			updateNeeded = true
		}

		if updateNeeded {
			logger.Info("Applying deployment update", "deployment", deploymentName)
			deployment, err = c.kubeclientset.AppsV1().Deployments(objectRef.Namespace).Update(ctx, deploymentCopy, metav1.UpdateOptions{})
			if err != nil {
				logger.Error(err, "Failed to update Deployment", "deployment", deploymentName)
				return err
			}
			logger.Info("Updated Deployment", "deployment", deploymentName)
		}
		logger.Info("Existing deployment container env vars", "env", deployment.Spec.Template.Spec.Containers[0].Env)
	}

	// Attempt status update with retries
	for i := 0; i < 3; i++ {
		myapp, err = c.myappclientset.UrmiV1alpha1().MyApps(objectRef.Namespace).Get(ctx, objectRef.Name, metav1.GetOptions{})
		if err != nil {
			logger.Error(err, "Failed to refresh MyApp before status update", "attempt", i+1)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if err := c.updateMyAppStatus(ctx, myapp, deployment); err != nil {
			logger.Error(err, "Failed to update MyApp status", "attempt", i+1)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		break
	}
	if err != nil {
		logger.Error(err, "Failed to update MyApp status after retries, proceeding to avoid infinite loop")
		// Do not return error to avoid requeuing
	}

	c.recorder.Event(myapp, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	logger.Info("Successfully synced MyApp", "objectName", objectRef)
	return nil
}

func equalEnvVars(a, b []corev1.EnvVar) bool {
	if len(a) != len(b) {
		klog.InfoS("Env length differs", "lenA", len(a), "lenB", len(b))
		return false
	}
	envMapA := make(map[string]string)
	envMapB := make(map[string]string)
	for _, env := range a {
		envMapA[env.Name] = env.Value
	}
	for _, env := range b {
		envMapB[env.Name] = env.Value
	}
	for name, valueA := range envMapA {
		valueB, exists := envMapB[name]
		if !exists || valueA != valueB {
			klog.InfoS("Env var differs for key", "name", name, "valueA", valueA, "valueB", valueB)
			return false
		}
	}
	return true
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (c *Controller) updateMyAppStatus(ctx context.Context, myapp *v1alpha1.MyApp, deployment *appsv1.Deployment) error {
	logger := klog.FromContext(ctx)

	// Fetch the latest MyApp resource directly from the API server
	latestMyApp, err := c.myappclientset.UrmiV1alpha1().MyApps(myapp.Namespace).Get(ctx, myapp.Name, metav1.GetOptions{})
	if err != nil {
		logger.Error(err, "Failed to fetch latest MyApp for status update", "namespace", myapp.Namespace, "name", myapp.Name)
		return err
	}

	// Create a copy of the latest MyApp for status update
	myappCopy := latestMyApp.DeepCopy()
	if deployment != nil {
		myappCopy.Status.AvailableReplicas = deployment.Status.AvailableReplicas
	} else {
		myappCopy.Status.AvailableReplicas = 0
	}

	// Update the status
	updatedMyApp, err := c.myappclientset.UrmiV1alpha1().MyApps(myappCopy.Namespace).UpdateStatus(ctx, myappCopy, metav1.UpdateOptions{})
	if err != nil {
		logger.Error(err, "Failed to update MyApp status", "namespace", myappCopy.Namespace, "name", myappCopy.Name)
		return err
	}

	logger.Info("Successfully updated MyApp status", "namespace", updatedMyApp.Namespace, "name", updatedMyApp.Name, "availableReplicas", updatedMyApp.Status.AvailableReplicas)
	return nil
}

func (c *Controller) enqueueMyApp(obj interface{}) {
	if objectRef, err := cache.ObjectToName(obj); err != nil {
		utilruntime.HandleError(err)
		return
	} else {
		c.workqueue.Add(objectRef)
	}
}

func (c *Controller) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	logger := klog.FromContext(context.Background())

	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleErrorWithContext(context.Background(), nil, "Error decoding object, invalid type", "type", fmt.Sprintf("%T", obj))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleErrorWithContext(context.Background(), nil, "Error decoding object tombstone, invalid type", "type", fmt.Sprintf("%T", tombstone.Obj))
			return
		}
		logger.V(4).Info("Recovered deleted object", "resourceName", object.GetName())
	}

	logger.V(4).Info("Processing object", "object", klog.KObj(object))
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		if ownerRef.Kind != "MyApp" {
			return
		}
		myapp, err := c.myappsLister.MyApps(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			logger.V(4).Info("Ignore orphaned object", "object", klog.KObj(object), "myapp", ownerRef.Name)
			return
		}
		c.enqueueMyApp(myapp)
	}
}

func newDeployment(myapp *v1alpha1.MyApp) *appsv1.Deployment {
	labels := map[string]string{
		"app": myapp.Name,
	}

	var replicas *int32
	if myapp.Spec.Replicas != nil {
		replicas = myapp.Spec.Replicas
	}

	authArg := "--auth=false"
	if myapp.Spec.Auth != nil && *myapp.Spec.Auth {
		authArg = "--auth=true"
	}

	image := "urmibiswas/book_project:v3"
	if myapp.Spec.Image != "" {
		image = myapp.Spec.Image
	}

	envVars := []corev1.EnvVar{
		{
			Name:  "JWT_SECRET",
			Value: myapp.Spec.JWTSecret,
		},
	}
	klog.InfoS("Creating deployment with env vars", "namespace", myapp.Namespace, "name", myapp.Name, "env", envVars)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myapp.Name + "-deployment",
			Namespace: myapp.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(myapp, v1alpha1.SchemeGroupVersion.WithKind("MyApp")),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "app",
							Image:           image,
							ImagePullPolicy: corev1.PullAlways,
							Command:         []string{"./main", "startProject"},
							Args:            []string{"--port=8080", authArg},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8080,
									Name:          "http",
									Protocol:      "TCP",
								},
							},
							Env: envVars,
						},
					},
				},
			},
		},
	}
}