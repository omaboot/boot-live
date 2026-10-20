// egl-probe: boot-live's GPU check helper, installed in the images as /usr/lib/omaboot/egl-probe.
//
// For each DRM render node given (default: every /dev/dri/renderD*), it opens the node through
// GBM, creates an EGL display and an OpenGL ES context (3.x first, else 2.0) with no window
// system, renders one frame (a full-screen triangle into a 64x64 offscreen texture) and reads a
// pixel back, then runs a compositor-like 1080p load (a wallpaper and two blended windows per
// frame) for about 10 s, timing each second. "--software" probes Mesa's surfaceless platform
// instead (llvmpipe).
//
// Output: plain key=value lines, one record per node, each starting with node= and ending with
// end=1. Keys: node, gbm_backend, egl, egl_vendor, api (gles3 | gles2), vendor, renderer,
// version, glsl, llvmpipe (1 for a software renderer), draw_ok, draw_pixel, draw_ms, bench_fps
// (the load's average), bench_min_fps (its slowest one-second window), bench_windows, bench_s
// (how long it ran), bench_error (why it didn't run or finish), error. Values are on one line. Adapted from omaboot-spikes' spike-look egl-probe, which rendered
// on i915, radeonsi, r600 and llvmpipe on the three test Macs.
//
// Build: cc -O2 -o egl-probe egl-probe.c -lEGL -lGLESv2 -lgbm
#include <EGL/egl.h>
#include <EGL/eglext.h>
#include <GLES2/gl2.h>
#include <fcntl.h>
#include <gbm.h>
#include <glob.h>
#include <stdio.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

static void kv(const char *k, const char *v) {
    printf("%s=", k);
    for (const char *p = v ? v : ""; *p; p++)
        putchar(((unsigned char)*p < 0x20) ? ' ' : *p);
    putchar('\n');
}

static double now_ms(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return t.tv_sec * 1e3 + t.tv_nsec / 1e6;
}

static GLuint program(const char *vsrc, const char *fsrc) {
    GLint ok = 0;
    GLuint vs = glCreateShader(GL_VERTEX_SHADER), fs = glCreateShader(GL_FRAGMENT_SHADER);
    glShaderSource(vs, 1, &vsrc, NULL); glCompileShader(vs); glGetShaderiv(vs, GL_COMPILE_STATUS, &ok);
    if (!ok) return 0;
    glShaderSource(fs, 1, &fsrc, NULL); glCompileShader(fs); glGetShaderiv(fs, GL_COMPILE_STATUS, &ok);
    if (!ok) return 0;
    GLuint pr = glCreateProgram();
    glAttachShader(pr, vs); glAttachShader(pr, fs); glBindAttribLocation(pr, 0, "p"); glLinkProgram(pr);
    glGetProgramiv(pr, GL_LINK_STATUS, &ok);
    return ok ? pr : 0;
}

static const char *VS = "attribute vec2 p; void main(){ gl_Position = vec4(p, 0.0, 1.0); }";
static const char *FS = "precision mediump float; void main(){ gl_FragColor = vec4(0.25, 0.5, 0.75, 1.0); }";

// One frame: a full-screen triangle into a 64x64 texture; 1 if the centre pixel is our colour.
static int draw_test(void) {
    double t0 = now_ms();
    GLuint pr = program(VS, FS);
    if (!pr) { kv("error", "test shaders did not compile or link"); return 0; }
    GLuint tex, fb;
    glGenTextures(1, &tex); glBindTexture(GL_TEXTURE_2D, tex);
    glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA, 64, 64, 0, GL_RGBA, GL_UNSIGNED_BYTE, NULL);
    glGenFramebuffers(1, &fb); glBindFramebuffer(GL_FRAMEBUFFER, fb);
    glFramebufferTexture2D(GL_FRAMEBUFFER, GL_COLOR_ATTACHMENT0, GL_TEXTURE_2D, tex, 0);
    if (glCheckFramebufferStatus(GL_FRAMEBUFFER) != GL_FRAMEBUFFER_COMPLETE) { kv("error", "framebuffer incomplete"); return 0; }
    static const GLfloat tri[] = {-1, -1, 3, -1, -1, 3};
    glViewport(0, 0, 64, 64); glClearColor(0, 0, 0, 1); glClear(GL_COLOR_BUFFER_BIT);
    glUseProgram(pr); glVertexAttribPointer(0, 2, GL_FLOAT, GL_FALSE, 0, tri); glEnableVertexAttribArray(0);
    glDrawArrays(GL_TRIANGLES, 0, 3); glFinish();
    unsigned char px[4] = {0};
    glReadPixels(32, 32, 1, 1, GL_RGBA, GL_UNSIGNED_BYTE, px);
    char got[64];
    snprintf(got, sizeof got, "%d,%d,%d,%d", px[0], px[1], px[2], px[3]);
    kv("draw_pixel", got);
    printf("draw_ms=%.1f\n", now_ms() - t0);
    int d0 = px[0] - 64, d1 = px[1] - 128, d2 = px[2] - 191;
    return d0 * d0 < 9 && d1 * d1 < 9 && d2 * d2 < 9 && px[3] == 255;
}

static const char *BVS = "attribute vec2 p; varying vec2 uv; uniform vec4 r;"
    "void main(){ uv = p * 0.5 + 0.5; gl_Position = vec4(r.xy + p * r.zw, 0.0, 1.0); }";
static const char *BFS = "precision mediump float; varying vec2 uv; uniform sampler2D t;"
    "void main(){ vec4 c = texture2D(t, uv); gl_FragColor = vec4(c.rgb * 0.9 + 0.05, 0.92); }";

// One frame of the compositor-like load: the wallpaper, then two blended windows.
static void frame(GLint r) {
    glDisable(GL_BLEND); glUniform4f(r, 0, 0, 1, 1); glDrawArrays(GL_TRIANGLE_STRIP, 0, 4);
    glEnable(GL_BLEND);
    glUniform4f(r, -0.3f, 0.1f, 0.55f, 0.6f); glDrawArrays(GL_TRIANGLE_STRIP, 0, 4);
    glUniform4f(r, 0.35f, -0.2f, 0.5f, 0.55f); glDrawArrays(GL_TRIANGLE_STRIP, 0, 4);
}

// A compositor-like load: a 1920x1080 wallpaper and two blended, textured windows per frame,
// into a 1920x1080 offscreen target, after a warm-up, for about 10 s in one-second windows (at
// most 10, and no new window after 10 s, so one stalled frame doesn't add nine more seconds).
// Prints bench_fps (frames over the whole run), bench_min_fps (the slowest window), bench_windows
// and bench_s, or bench_error. The slowest window is what the verdict reads: an average over 10 s
// hides a GPU that stalls for a second or two.
static void bench_1080(void) {
    enum { W = 1920, H = 1080, WINDOWS = 10 };
    static unsigned char px[W * H * 4];
    for (int i = 0; i < W * H * 4; i++) px[i] = (unsigned char)(i * 7 + (i >> 11));
    while (glGetError() != GL_NO_ERROR) {} // only the load's own errors count below
    GLuint pr = program(BVS, BFS);
    if (!pr) { kv("bench_error", "load shaders did not compile or link"); return; }
    GLuint tex[2], fb;
    glGenTextures(2, tex);
    for (int k = 0; k < 2; k++) {
        glBindTexture(GL_TEXTURE_2D, tex[k]);
        glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_LINEAR);
        glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MAG_FILTER, GL_LINEAR);
        glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_S, GL_CLAMP_TO_EDGE);
        glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_T, GL_CLAMP_TO_EDGE);
        glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA, W, H, 0, GL_RGBA, GL_UNSIGNED_BYTE, k ? px : NULL);
    }
    glGenFramebuffers(1, &fb); glBindFramebuffer(GL_FRAMEBUFFER, fb);
    glFramebufferTexture2D(GL_FRAMEBUFFER, GL_COLOR_ATTACHMENT0, GL_TEXTURE_2D, tex[0], 0);
    if (glCheckFramebufferStatus(GL_FRAMEBUFFER) != GL_FRAMEBUFFER_COMPLETE) { kv("bench_error", "1080p framebuffer incomplete"); return; }
    static const GLfloat quad[] = {-1, -1, 1, -1, -1, 1, 1, 1};
    glViewport(0, 0, W, H); glUseProgram(pr);
    glBindTexture(GL_TEXTURE_2D, tex[1]);
    glUniform1i(glGetUniformLocation(pr, "t"), 0);
    GLint r = glGetUniformLocation(pr, "r");
    glVertexAttribPointer(0, 2, GL_FLOAT, GL_FALSE, 0, quad); glEnableVertexAttribArray(0);
    glBlendFunc(GL_SRC_ALPHA, GL_ONE_MINUS_SRC_ALPHA);
    glFinish();
    // Untimed warm-up frames: the first draws compile the driver's shader variants (blend off and
    // on) and, on a live stick read on demand, page the driver in over USB; timing them read
    // 1.3 fps on an iMac10,1 whose RV730 does hundreds.
    for (int w = 0; w < 3; w++) frame(r);
    glFinish();
    double t0 = now_ms(), low = -1;
    long frames = 0;
    int windows = 0;
    while (windows < WINDOWS && now_ms() - t0 < WINDOWS * 1000.0) {
        double w0 = now_ms(), ms;
        int n = 0;
        do {
            frame(r);
            glFinish();
            n++;
        } while ((ms = now_ms() - w0) < 1000);
        double fps = n * 1000.0 / ms;
        if (low < 0 || fps < low) low = fps;
        frames += n;
        windows++;
    }
    double total = now_ms() - t0;
    GLenum err = glGetError();
    if (err != GL_NO_ERROR) {
        char e[64];
        snprintf(e, sizeof e, "GL error 0x%x during the load", err);
        kv("bench_error", e);
        return;
    }
    printf("bench_fps=%.1f\n", frames * 1000.0 / total);
    printf("bench_min_fps=%.1f\n", low);
    printf("bench_windows=%d\n", windows);
    printf("bench_s=%.1f\n", total / 1000);
}

// A GLES context of the given major version; 1 if it is current.
static int context(EGLDisplay dpy, int major, EGLContext *out) {
    EGLint renderable = major >= 3 ? EGL_OPENGL_ES3_BIT : EGL_OPENGL_ES2_BIT;
    EGLint cfgattr[] = {EGL_SURFACE_TYPE, 0, EGL_RENDERABLE_TYPE, renderable, EGL_NONE};
    EGLConfig cfg;
    EGLint n = 0;
    if (!eglBindAPI(EGL_OPENGL_ES_API) || !eglChooseConfig(dpy, cfgattr, &cfg, 1, &n) || n < 1) return 0;
    EGLint ctxattr[] = {EGL_CONTEXT_MAJOR_VERSION, major, EGL_NONE};
    EGLContext ctx = eglCreateContext(dpy, cfg, EGL_NO_CONTEXT, ctxattr);
    if (ctx == EGL_NO_CONTEXT) return 0;
    if (!eglMakeCurrent(dpy, EGL_NO_SURFACE, EGL_NO_SURFACE, ctx)) {
        eglDestroyContext(dpy, ctx);
        return 0;
    }
    *out = ctx;
    return 1;
}

static void probe_display(EGLDisplay dpy) {
    EGLint maj = 0, min = 0;
    if (dpy == EGL_NO_DISPLAY || !eglInitialize(dpy, &maj, &min)) {
        char e[64];
        snprintf(e, sizeof e, "eglInitialize failed (0x%x)", eglGetError());
        kv("error", e);
        return;
    }
    printf("egl=%d.%d\n", maj, min);
    kv("egl_vendor", eglQueryString(dpy, EGL_VENDOR));
    const char *ext = eglQueryString(dpy, EGL_EXTENSIONS);
    if (!ext || !strstr(ext, "EGL_KHR_surfaceless_context")) {
        kv("error", "no EGL_KHR_surfaceless_context");
        eglTerminate(dpy);
        return;
    }
    EGLContext ctx = EGL_NO_CONTEXT;
    const char *api = NULL;
    if (context(dpy, 3, &ctx)) api = "gles3";
    else if (context(dpy, 2, &ctx)) api = "gles2";
    if (!api) {
        char e[64];
        snprintf(e, sizeof e, "no OpenGL ES context (0x%x)", eglGetError());
        kv("error", e);
        eglTerminate(dpy);
        return;
    }
    kv("api", api);
    const char *renderer = (const char *)glGetString(GL_RENDERER);
    kv("vendor", (const char *)glGetString(GL_VENDOR));
    kv("renderer", renderer);
    kv("version", (const char *)glGetString(GL_VERSION));
    kv("glsl", (const char *)glGetString(GL_SHADING_LANGUAGE_VERSION));
    int soft = renderer && (strstr(renderer, "llvmpipe") || strstr(renderer, "softpipe") || strstr(renderer, "swrast"));
    printf("llvmpipe=%d\n", soft ? 1 : 0);
    printf("draw_ok=%d\n", draw_test());
    bench_1080();
    eglMakeCurrent(dpy, EGL_NO_SURFACE, EGL_NO_SURFACE, EGL_NO_CONTEXT);
    eglDestroyContext(dpy, ctx);
    eglTerminate(dpy);
}

static void probe_node(const char *node) {
    kv("node", node);
    if (strcmp(node, "--software") == 0) {
        probe_display(eglGetPlatformDisplay(EGL_PLATFORM_SURFACELESS_MESA, EGL_DEFAULT_DISPLAY, NULL));
    } else {
        int fd = open(node, O_RDWR | O_CLOEXEC);
        if (fd < 0) {
            kv("error", "can't open the render node");
        } else {
            struct gbm_device *gbm = gbm_create_device(fd);
            if (!gbm) {
                kv("error", "gbm_create_device failed");
            } else {
                kv("gbm_backend", gbm_device_get_backend_name(gbm));
                probe_display(eglGetPlatformDisplay(EGL_PLATFORM_GBM_KHR, gbm, NULL));
                gbm_device_destroy(gbm);
            }
            close(fd);
        }
    }
    printf("end=1\n");
    fflush(stdout);
}

int main(int argc, char **argv) {
    if (argc > 1) {
        for (int i = 1; i < argc; i++) probe_node(argv[i]);
        return 0;
    }
    glob_t g;
    if (glob("/dev/dri/renderD*", 0, NULL, &g) == 0) {
        for (size_t i = 0; i < g.gl_pathc; i++) probe_node(g.gl_pathv[i]);
        globfree(&g);
    }
    return 0;
}
