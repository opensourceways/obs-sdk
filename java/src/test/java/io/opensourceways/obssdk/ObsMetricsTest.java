package io.opensourceways.obssdk;

import io.opensourceways.obssdk.context.RequestContext;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class ObsMetricsTest {

    private static ObsSdkConfig cfg(String... kv) {
        ObsSdkConfig.Builder b = ObsSdkConfig.builder();
        for (int i = 0; i + 1 < kv.length; i += 2) {
            switch (kv[i]) {
                case "service" -> b.service(kv[i + 1]);
                case "env" -> b.env(kv[i + 1]);
                case "instance" -> b.instance(kv[i + 1]);
                case "community" -> b.community(kv[i + 1]);
                case "namespace" -> b.namespace(kv[i + 1]);
                default -> throw new IllegalArgumentException("unknown cfg key: " + kv[i]);
            }
        }
        return b.build();
    }

    /** 抓取 scrape 文本里某一 series 家族的数值行（非 # 注释行）。 */
    private static List<String> samples(String text, String family) {
        List<String> out = new ArrayList<>();
        for (String line : text.split("\n")) {
            if (line.startsWith(family) && !line.startsWith("#")) {
                out.add(line);
            }
        }
        return out;
    }

    private static double value(String sampleLine) {
        int brace = sampleLine.indexOf('}');
        return Double.parseDouble(sampleLine.substring(brace + 1).trim());
    }

    @Test
    void counter_commonTag加community双层注入() {
        ObsMetrics m = ObsMetrics.of(cfg("service", "review", "env", "test",
                "instance", "pod-1", "community", "openeuler"));
        ObsMetrics.CounterVec c = m.counter("events", "事件数", "kind");

        c.inc(1, "pr");
        try (RequestContext.Scope ignored = RequestContext.push("mindspore", "r-1", "t-1")) {
            c.inc(2, "pr");
        }

        String text = m.text();
        List<String> ev = samples(text, "events_total");
        assertEquals(2, ev.size(), "默认与覆盖两种 community 应各自成一条 series");

        String defLine = lineWith(ev, "community=\"openeuler\"");
        assertTrue(defLine.contains("service=\"review\""), "const label service 应在： " + defLine);
        assertTrue(defLine.contains("env=\"test\""));
        assertTrue(defLine.contains("instance=\"pod-1\""));
        assertTrue(defLine.contains("kind=\"pr\""));
        assertEquals(1.0, value(defLine));

        String ovLine = lineWith(ev, "community=\"mindspore\"");
        assertTrue(ovLine.contains("service=\"review\""));
        assertEquals(2.0, value(ovLine));
    }

    @Test
    void gauge与histogram() {
        ObsMetrics m = ObsMetrics.of(cfg("service", "s", "community", "openeuler"));
        ObsMetrics.GaugeVec g = m.gauge("in_flight", "在飞请求数");
        g.set(3);

        ObsMetrics.HistogramVec h = m.histogram("latency", "处理延迟");
        h.observe(0.1);
        h.observe(0.3);

        String text = m.text();
        // gauge 名不带后缀
        List<String> gauges = samples(text, "in_flight");
        assertEquals(1, gauges.size());
        assertTrue(gauges.get(0).contains("community=\"openeuler\""));
        assertEquals(3.0, value(gauges.get(0)));

        // Timer 自动追加 _seconds，并产出 _count/_sum
        List<String> counts = samples(text, "latency_seconds_count");
        assertEquals(1, counts.size());
        assertEquals(2.0, value(counts.get(0)));
        List<String> sums = samples(text, "latency_seconds_sum");
        assertEquals(1, sums.size());
        assertTrue(Math.abs(value(sums.get(0)) - 0.4) < 1e-6);
    }

    @Test
    void namespace前缀() {
        ObsMetrics m = ObsMetrics.of(cfg("service", "s", "community", "openeuler", "namespace", "obs"));
        m.counter("events", "事件数").inc(1);
        assertTrue(m.text().contains("obs_events_total"), m.text());
    }

    /**
     * 回归：四个部署级字段在未显式配置时也必须出现在 label 集里。
     *
     * <p>此前 {@code fromEnvironment()} 无兜底，取值可能为 null；{@code community} 为 null 时
     * {@code Tag.of} 直接 NPE，{@code service/env/instance} 为 null 时 commonTags 被整段跳过 ——
     * 同一份大盘查询，Java 服务比 Go 服务少几个 label，按 label 过滤时静默漏掉这些服务。
     */
    @Test
    void 未显式配置时四个部署级label仍非空() {
        ObsMetrics m = ObsMetrics.of(ObsSdkConfig.builder().build());
        m.counter("events", "事件数").inc(1);

        List<String> ev = samples(m.text(), "events_total");
        assertEquals(1, ev.size());
        String line = ev.get(0);
        for (String label : new String[]{"service", "env", "instance", "community"}) {
            assertTrue(line.contains(label + "=\""), "缺少 label " + label + "： " + line);
            assertFalse(line.contains(label + "=\"\""), label + " 值为空： " + line);
        }
    }

    @Test
    void histogram自定义桶() {
        ObsMetrics m = ObsMetrics.of(cfg("service", "s", "community", "openeuler"));
        m.histogram("latency", "处理延迟", new double[]{0.1, 0.5}).observe(0.05);
        String text = m.text();
        assertTrue(text.contains("latency_seconds_bucket{"), text);
        assertTrue(text.contains("le=\"0.1\""), text);
        assertTrue(text.contains("le=\"0.5\""), text);
    }

    private static String lineWith(List<String> lines, String sub) {
        return lines.stream()
                .filter(l -> l.contains(sub))
                .findFirst()
                .orElseThrow(() -> new AssertionError("缺少包含 " + sub + " 的 series，实际： " + lines));
    }
}
