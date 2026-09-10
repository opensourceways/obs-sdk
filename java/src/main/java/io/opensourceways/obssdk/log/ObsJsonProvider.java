package io.opensourceways.obssdk.log;

import ch.qos.logback.classic.Level;
import ch.qos.logback.classic.spi.ILoggingEvent;
import ch.qos.logback.classic.spi.IThrowableProxy;
import ch.qos.logback.classic.spi.ThrowableProxyUtil;
import com.fasterxml.jackson.core.JsonGenerator;
import net.logstash.logback.composite.AbstractJsonProvider;

import java.io.IOException;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.util.Locale;
import java.util.Map;

/**
 * obs-sdk 的 logstash-logback-encoder provider：按 spec/log-format.md 输出固定字段。
 *
 * <p>为什么不用 encoder 自带的 provider 逐项拼装：
 * <ul>
 *   <li>{@code <logLevel/>} 只能输出大写 {@code INFO}，契约要求小写，且 7.4 没有任何
 *       配置项可改大小写（{@code <logLevelValue/>} 输出的是数字 20000，不是名字）；</li>
 *   <li>encode 的字段名走 {@code LogstashFieldNames}，{@code LoggingEventCompositeJsonEncoder}
 *       没有 {@code setFieldNames}，只能逐个 provider 嵌套配置，样例会变得难以维护；</li>
 *   <li>不配 {@code <stackTrace/>} 时 throwable 会被整条丢弃 —— 错误日志直接丢失原因，
 *       这是最要命的一条。</li>
 * </ul>
 * 因此把「固定字段」这一层的所有权收到 SDK 内：一个 provider 按契约顺序输出全部固定字段，
 * 接入方只需在配置里写一行 {@code <provider class="...ObsJsonProvider"/>}。
 *
 * <p>字段顺序：{@code time / level / msg / service / env / instance / community /
 * request_id / trace_id / span_id / logger / error}。其中
 * {@code service / env / instance / community} 来自 {@link ObsLogging#init} 写入的 MDC，
 * 其余请求级字段由中间件经 {@link ObsLogging#enrich} 写入，空值一律省略
 * （{@code trace_id} / {@code span_id} 为二期预留，首期通常不出现）。
 *
 * <p>业务字段不属于本 provider 的职责：接入方可在本 provider 之后追加
 * {@code <keyValuePairs/>} 或 {@code <mdc/>} 等 provider，输出会落在固定字段之后。
 */
public class ObsJsonProvider extends AbstractJsonProvider<ILoggingEvent> {

    /** 与 spec/log-format.md 一致：固定毫秒精度、UTC、零偏移输出 Z（非 +00:00）。 */
    private static final DateTimeFormatter TIME_FORMATTER =
            DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ss.SSSXXX").withZone(ZoneOffset.UTC);

    /** MDC 中属于固定契约的键，按契约顺序输出；空值省略。 */
    private static final String[] MDC_CONTRACT_KEYS = {
            ObsLogging.MDC_SERVICE,
            ObsLogging.MDC_ENV,
            ObsLogging.MDC_INSTANCE,
            ObsLogging.MDC_COMMUNITY,
            ObsLogging.MDC_REQUEST_ID,
            ObsLogging.MDC_TRACE_ID,
            ObsLogging.MDC_SPAN_ID,
    };

    @Override
    public void writeTo(JsonGenerator generator, ILoggingEvent event) throws IOException {
        generator.writeStringField("time", TIME_FORMATTER.format(Instant.ofEpochMilli(event.getTimeStamp())));
        generator.writeStringField("level", levelName(event.getLevel()));
        generator.writeStringField("msg", event.getFormattedMessage());

        Map<String, String> mdc = event.getMDCPropertyMap();
        for (String key : MDC_CONTRACT_KEYS) {
            String value = mdc == null ? null : mdc.get(key);
            if (value != null && !value.isEmpty()) {
                generator.writeStringField(key, value);
            }
        }

        String caller = callerLocation(event);
        if (caller != null) {
            generator.writeStringField("logger", caller);
        }

        IThrowableProxy throwable = event.getThrowableProxy();
        if (throwable != null) {
            // 契约允许 error 携带完整堆栈（多行由 JSON 转义为 \n，仍是单行 JSON）；
            // 该字段取值逐次不同，不适合聚合，需要按错误类型聚合时用 msg + 业务字段。
            generator.writeStringField("error", ThrowableProxyUtil.asString(throwable));
        }
    }

    /**
     * 异步 appender（AsyncAppender）下调用点数据必须在业务线程上提前提取，
     * 否则写盘线程拿到的栈已不是原始调用点。与 logstash 自带 provider 的同一处理保持一致。
     */
    @Override
    public void prepareForDeferredProcessing(ILoggingEvent event) {
        event.getCallerData();
    }

    /** logback 级别 → 契约小写枚举。TRACE 归入 debug（契约枚举只有 debug/info/warn/error）。 */
    private static String levelName(Level level) {
        if (level == null) {
            return "info";
        }
        if (Level.TRACE.equals(level)) {
            return "debug";
        }
        return level.toString().toLowerCase(Locale.ROOT);
    }

    /** 调用位置，形如 {@code ReviewSvc.java:51}（对齐 Go 侧 logger 字段的 file:line 语义）。 */
    private static String callerLocation(ILoggingEvent event) {
        StackTraceElement[] callerData = event.getCallerData();
        if (callerData == null || callerData.length == 0) {
            return null;
        }
        StackTraceElement frame = callerData[0];
        if (frame.getFileName() == null) {
            return null;
        }
        return frame.getFileName() + ":" + frame.getLineNumber();
    }
}
