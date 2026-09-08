package io.opensourceways.obssdk;

import io.micrometer.core.instrument.Counter;
import io.micrometer.core.instrument.Gauge;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.Tag;
import io.micrometer.core.instrument.Tags;
import io.micrometer.core.instrument.Timer;
import io.micrometer.prometheusmetrics.PrometheusConfig;
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry;
import io.opensourceways.obssdk.context.RequestContext;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicDouble;

/**
 * 指标装配：对 Micrometer + Prometheus registry 的薄封装，语义与 Go/Python/Node SDK 对齐
 * （见 spec/metrics-format.md、common-fields.md）。
 *
 * <ul>
 *   <li>service/env/instance 三个部署级字段注册为 <b>common tags</b>（const label）；</li>
 *   <li><b>community 建模为普通可变 label</b>：值取请求上下文覆盖（可信判定点显式写入），
 *       无覆盖时回退部署默认 —— 「注册一次两用」，单社区/多社区共用同一注册点。</li>
 *   <li>{@code namespace} 可选：给指标名加前缀（跨服务共享 SDK 时用）。</li>
 * </ul>
 *
 * <p>注意：本 SDK 不重复造 HTTP 服务端指标 —— Java 服务通常走 Spring Boot Actuator +
 * Micrometer 官方 server instrumentation 上报（starter 对齐），本类只提供业务 counter/gauge/histogram。
 */
public final class ObsMetrics {

    private final PrometheusMeterRegistry registry;
    private final String service;
    private final String envName;
    private final String instance;
    private final String defaultCommunity;
    private final String namespace;

    private ObsMetrics(ObsSdkConfig cfg) {
        this.registry = new PrometheusMeterRegistry(PrometheusConfig.DEFAULT);
        this.service = cfg.service();
        this.envName = cfg.envName();
        this.instance = cfg.instance();
        this.defaultCommunity = cfg.community();
        this.namespace = cfg.namespace();

        List<Tag> common = new ArrayList<>();
        if (service != null) {
            common.add(Tag.of("service", service));
        }
        if (envName != null) {
            common.add(Tag.of("env", envName));
        }
        if (instance != null) {
            common.add(Tag.of("instance", instance));
        }
        if (!common.isEmpty()) {
            registry.config().commonTags(Tags.of(common));
        }
    }

    public static ObsMetrics of(ObsSdkConfig cfg) {
        return new ObsMetrics(cfg);
    }

    public PrometheusMeterRegistry meterRegistry() {
        return registry;
    }

    /** Prometheus text 格式快照（同其它语言 SDK 的 {@code text()}，测试与自检用）。 */
    public String text() {
        return registry.scrape();
    }

    /**
     * 注册业务 counter。注意 Micrometer/Prometheus 会自动为 Counter 追加 {@code _total} 后缀，
     * 这里传基础名（如 {@code "http_server_requests"}）。
     *
     * @param name        指标基础名
     * @param help        帮助文本
     * @param labelNames  业务 label 名（不含 community，community 由本 SDK 自动排在首位）
     */
    public CounterVec counter(String name, String help, String... labelNames) {
        return new CounterVec(meterName(name), help, labelNames);
    }

    /** 注册 gauge（exported 名不带后缀）。 */
    public GaugeVec gauge(String name, String help, String... labelNames) {
        return new GaugeVec(meterName(name), help, labelNames);
    }

    /**
     * 注册业务 histogram（基于 Micrometer Timer，自动追加 {@code _seconds} 后缀 + {@code _count/_sum}）。
     * Timer 的 label 与 counter 相同语义：community 自动排在首位。
     *
     * @param name   指标基础名，如 {@code "http_server_request_duration"}
     * @param help   帮助文本
     * @param buckets 可选 SLA 桶（秒）；不传则不产生 {@code _bucket} 系列
     * @param labelNames 业务 label 名
     */
    public HistogramVec histogram(String name, String help, double[] buckets, String... labelNames) {
        return new HistogramVec(meterName(name), help, labelNames, buckets);
    }

    public HistogramVec histogram(String name, String help, String... labelNames) {
        return new HistogramVec(meterName(name), help, labelNames, null);
    }

    private String meterName(String base) {
        return (namespace == null || namespace.isEmpty()) ? base : namespace + "_" + base;
    }

    // ---- 内部工具：community 值解析（覆盖优先于默认） ----

    private static String communityValue(String defaultCommunity) {
        return RequestContext.communityOverride().orElse(defaultCommunity);
    }

    private static List<Tag> tags(String defaultCommunity, String[] labelNames, String[] values) {
        if (labelNames.length != values.length) {
            throw new IllegalArgumentException("label 数量不匹配: names=" + labelNames.length + " values=" + values.length);
        }
        List<Tag> tags = new ArrayList<>(labelNames.length + 1);
        String community = communityValue(defaultCommunity);
        if (community != null) {
            tags.add(Tag.of("community", community));
        }
        for (int i = 0; i < labelNames.length; i++) {
            tags.add(Tag.of(labelNames[i], values[i] == null ? "" : values[i]));
        }
        return tags;
    }

    // ---- vec 类型 ----

    /** 业务 counter 句柄：{@code inc} 系列方法自动带 community（context 覆盖或默认）。 */
    public final class CounterVec {
        private final String name;
        private final String help;
        private final String[] labelNames;

        private CounterVec(String name, String help, String[] labelNames) {
            this.name = name;
            this.help = help;
            this.labelNames = labelNames;
        }

        public void inc(String... labelValues) {
            inc(1.0, labelValues);
        }

        public void inc(double amount, String... labelValues) {
            registry.counter(name, tags(ObsMetrics.this.defaultCommunity, labelNames, labelValues)).increment(amount);
        }
    }

    /** 业务 gauge 句柄：按 (community + 业务 label) 组合各自维护一个可写状态。 */
    public final class GaugeVec {
        private final String name;
        private final String help;
        private final String[] labelNames;
        private final ConcurrentHashMap<String, AtomicDouble> states = new ConcurrentHashMap<>();

        private GaugeVec(String name, String help, String[] labelNames) {
            this.name = name;
            this.help = help;
            this.labelNames = labelNames;
        }

        public void set(double value, String... labelValues) {
            child(labelValues).set(value);
        }

        private AtomicDouble child(String... labelValues) {
            if (labelValues.length != labelNames.length) {
                throw new IllegalArgumentException(
                        "label 数量不匹配: names=" + labelNames.length + " values=" + labelValues.length);
            }
            String community = communityValue(ObsMetrics.this.defaultCommunity);
            List<Tag> tags = new ArrayList<>(labelNames.length + 1);
            List<String> keyParts = new ArrayList<>(labelNames.length + 1);
            keyParts.add(community == null ? "" : community);
            if (community != null) {
                tags.add(Tag.of("community", community));
            }
            for (int i = 0; i < labelNames.length; i++) {
                String v = labelValues[i] == null ? "" : labelValues[i];
                tags.add(Tag.of(labelNames[i], v));
                keyParts.add(v);
            }
            String key = keyParts.toString();
            AtomicDouble state = states.get(key);
            if (state == null) {
                AtomicDouble created = new AtomicDouble();
                Gauge.builder(name, created, AtomicDouble::doubleValue)
                        .description(help)
                        .tags(tags)
                        .register(registry);
                AtomicDouble raced = states.putIfAbsent(key, created);
                state = raced == null ? created : raced;
            }
            return state;
        }
    }

    /** 业务 histogram 句柄：基于 Timer，自动带 community。 */
    public final class HistogramVec {
        private final String name;
        private final String help;
        private final String[] labelNames;
        private final double[] buckets;

        private HistogramVec(String name, String help, String[] labelNames, double[] buckets) {
            this.name = name;
            this.help = help;
            this.labelNames = labelNames;
            this.buckets = buckets;
        }

        /** 观测一次耗时，单位秒。 */
        public void observe(double seconds, String... labelValues) {
            Timer timer = timer(labelValues);
            timer.record(Duration.ofNanos(Math.round(seconds * 1_000_000_000d)));
        }

        public void observe(Duration duration, String... labelValues) {
            timer(labelValues).record(duration);
        }

        private Timer timer(String... labelValues) {
            List<Tag> tags = tags(ObsMetrics.this.defaultCommunity, labelNames, labelValues);
            Timer.Builder builder = Timer.builder(name).description(help).tags(tags);
            if (buckets != null && buckets.length > 0) {
                Duration[] sla = Arrays.stream(buckets)
                        .mapToObj(b -> Duration.ofNanos(Math.round(b * 1_000_000_000d)))
                        .toArray(Duration[]::new);
                builder.serviceLevelObjectives(sla);
            }
            return builder.register(registry);
        }
    }
}
