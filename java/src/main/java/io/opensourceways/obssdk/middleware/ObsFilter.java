package io.opensourceways.obssdk.middleware;

import io.opensourceways.obssdk.context.RequestContext;
import io.opensourceways.obssdk.log.ObsLogging;
import jakarta.servlet.Filter;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.ServletRequest;
import jakarta.servlet.ServletResponse;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;

import java.io.IOException;
import java.util.UUID;
import java.util.function.Function;

/**
 * Java 版请求上下文中间件（对齐 Go http/gin、Python、Node 的 middleware）。
 *
 * <p>职责与其它语言 SDK 一致：注入 {@code request_id}（沿用入站 {@code X-Request-Id} 或生成），
 * 在可信判定点解析请求级 community 后写入 {@link RequestContext}，并刷新日志 MDC。
 *
 * <p><b>安全约束</b>（spec/community-values.md）：community 不从不加鉴别的入参盲取，
 * 必须经 {@code resolveCommunity} —— 由服务自己基于路由前缀 / 认证主体 / 白名单判定；
 * 不提供 resolver 时退化为部署级单社区（不做请求级覆盖）。
 *
 * <p><b>不做</b> HTTP 服务端指标埋点：Java 服务一般走 Spring Boot Actuator +
 * Micrometer 官方 server instrumentation（starter 对齐），SDK 不重复造轮子。
 *
 * <p>Spring Boot 注册示例：
 * <pre>{@code
 * @Bean
 * public FilterRegistrationBean<ObsFilter> obsFilter() {
 *     FilterRegistrationBean<ObsFilter> reg = new FilterRegistrationBean<>();
 *     reg.setFilter(new ObsFilter(req -> req.getRequestURI().startsWith("/mindspore") ? "mindspore" : null));
 *     reg.addUrlPatterns("/*");
 *     reg.setOrder(Ordered.HIGHEST_PRECEDENCE);
 *     return reg;
 * }
 * }</pre>
 */
public class ObsFilter implements Filter {

    public static final String HEADER_REQUEST_ID = "X-Request-Id";
    public static final String HEADER_TRACE_ID = "X-Trace-Id";

    private final Function<HttpServletRequest, String> resolveCommunity;

    public ObsFilter() {
        this(req -> null);
    }

    public ObsFilter(Function<HttpServletRequest, String> resolveCommunity) {
        this.resolveCommunity = resolveCommunity;
    }

    @Override
    public void doFilter(ServletRequest request, ServletResponse response, FilterChain chain)
            throws IOException, ServletException {
        HttpServletRequest http = (HttpServletRequest) request;
        HttpServletResponse res = (HttpServletResponse) response;

        String requestId = http.getHeader(HEADER_REQUEST_ID);
        if (requestId == null || requestId.isEmpty()) {
            requestId = UUID.randomUUID().toString().replace("-", "");
        }
        // trace_id 预留：只在可信入站头存在时透传，不主动生成（首期不落 span）
        String traceId = http.getHeader(HEADER_TRACE_ID);
        String community = resolveCommunity.apply(http);

        try (RequestContext.Scope scope = RequestContext.push(community, requestId, traceId)) {
            ObsLogging.enrich(RequestContext.current().orElse(null));
            chain.doFilter(request, response);
        } finally {
            ObsLogging.clearRequestScope();
        }
    }
}
