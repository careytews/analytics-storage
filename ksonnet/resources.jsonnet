//
// Definition for storage loader
//

// Import KSonnet library.
local k = import "ksonnet.beta.2/k.libsonnet";

// Short-cuts to various objects in the KSonnet library.
local depl = k.extensions.v1beta1.deployment;
local container = depl.mixin.spec.template.spec.containersType;
local mount = container.volumeMountsType;
local volume = depl.mixin.spec.template.spec.volumesType;
local resources = container.resourcesType;
local env = container.envType;
local secretDisk = volume.mixin.secret;
local annotations = depl.mixin.spec.template.metadata.annotations;

local worker(config) = {

    local version = import "version.jsonnet",
    local pgm = "analytics-storage",

    name: pgm,
    namespace: config.namespace,
    labels: {app:pgm, component:"analytics"},
    images: [config.containerBase + "/analytics-storage:" + version],

    input: config.workers.queues.storage.input,
    output: config.workers.queues.storage.output,

    // Volumes - single volume containing the key secret
    volumeMounts:: [
    ] + if config.cloud == "gcp" then [
        mount.new("keys", "/key", true)
    ] else [] + if config.cloud == "aws" then [
        mount.new("keys", "/root/.aws/credentials", true) + mount.subPath("credentials")
    ] else [],

    // Environment variables
    envs:: [

        // AMQP Broker URL
        env.new("AMQP_BROKER", "amqp://guest:guest@amqp:5672/"),

        // Platform
		env.new("PLATFORM", config.cloud),

        // Storage table settings
        env.new("STORAGE_PROJECT", config.project),
        env.new("STORAGE_BUCKET", config.storage.bucket),
        env.new("STORAGE_BASEDIR", config.storage.basedir),
        env.new("MAX_BATCH", "64M"),
        env.new("MAX_TIME", "1800")

    ] + if config.cloud == "gcp" then [
        // Pathname of key file.
        env.new("KEY", "/key/private.json")
    ] else [] + if config.cloud == "aws" then [
		// Tell it to load ~/.aws/credentials
		env.new("AWS_SDK_LOAD_CONFIG", "true"),
		env.new("AWS_BUCKET_REGION", config.storage.bucketRegion),
	] else [],

    // Container definition.
    containers:: [
        container.new(self.name, self.images[0]) +
            container.volumeMounts(self.volumeMounts) +
            container.env(self.envs) +
            container.args([self.input] +
                           std.map(function(x) "output:" + x,
                                   self.output)) +
            container.mixin.resources.limits({
                memory: "1G", cpu: "0.85"
            }) +
            container.mixin.resources.requests({
                memory: "1G", cpu: "0.8"
            })
    ],

    // Volumes
    volumes:: [
        volume.name("keys") +
            secretDisk.secretName("analytics-storage-keys")
    ],

    // Deployment definition.
    deployments:: [
    depl.new(self.name,
                 config.workers.replicas.storage.min,
                 self.containers,
                 self.labels) +
            depl.mixin.spec.template.spec.volumes(self.volumes) +
            depl.mixin.metadata.namespace($.namespace) +
	annotations({"prometheus.io/scrape": "true",
		     "prometheus.io/port": "8080"})
    ],

    resources:: 
		if config.options.includeAnalytics then
			self.deployments
		else [],
};

[worker]
