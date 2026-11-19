use super::service::MiddlewareConfig;

#[derive(Clone, Debug)]
pub struct LozaLayer {
    pub config: MiddlewareConfig,
}

impl LozaLayer {
    pub fn new(config: MiddlewareConfig) -> Self {
        Self { config }
    }
}

#[cfg(feature = "tower")]
impl<S> tower::Layer<S> for LozaLayer {
    type Service = super::service::LozaService<S>;

    fn layer(&self, inner: S) -> Self::Service {
        // Keep `LozaLayer::new(config)` source-compatible: events are emitted
        // through the process-wide default logger. Callers that need a
        // specific logger can construct a `LozaService` directly.
        super::service::LozaService::new(inner, self.config.clone(), crate::default())
    }
}
