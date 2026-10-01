// Capability check only, on the approved isolated GitHub-hosted Mac.
// No windows, captures, terminal launches, or system settings are changed.
// Attributes follow Kitty v0.49.1 glfw/nsgl_context.m and kitty/glfw.c.
#import <Cocoa/Cocoa.h>
#import <OpenGL/gl3.h>

static int emit(NSMutableDictionary *report, int status) {
    NSError *error = nil;
    NSData *data = [NSJSONSerialization dataWithJSONObject:report
        options:NSJSONWritingPrettyPrinted | NSJSONWritingSortedKeys error:&error];
    if (!data || fwrite(data.bytes, 1, data.length, stdout) != data.length ||
        fputc('\n', stdout) == EOF || fflush(stdout) != 0) return 74;
    return status;
}

static NSString *glText(GLenum key) {
    const GLubyte *value = glGetString(key);
    NSString *text = value ? [NSString stringWithUTF8String:(const char *)value] : nil;
    if (!text) return @"";
    return [text substringToIndex:MIN(text.length, 512)];
}

int main(void) {
    @autoreleasepool {
        NSDictionary *env = NSProcessInfo.processInfo.environment;
        if (![env[@"GITHUB_ACTIONS"] isEqual:@"true"] ||
            ![env[@"RUNNER_ENVIRONMENT"] isEqual:@"github-hosted"] ||
            ![env[@"RUNNER_OS"] isEqual:@"macOS"]) {
            fputs("refusing local OpenGL execution\n", stderr);
            return 64;
        }
        NSMutableDictionary *report = [@{
            @"scope": @"Kitty-equivalent NSOpenGL capability, not native image proof",
            @"workflow_sha": env[@"GITHUB_SHA"] ?: @"",
            @"runner_arch": env[@"RUNNER_ARCH"] ?: @"",
            @"system": NSProcessInfo.processInfo.operatingSystemVersionString,
            @"requested": @"accelerated, closest, offline, graphics switching, core 3.2 profile, RGB24/A8, depth0/stencil0, double buffer, samples0",
            @"required_gl_version": @"3.3 or later, core profile",
            @"pixel_format_created": @NO, @"context_created": @NO,
            @"capable": @NO
        } mutableCopy];
        NSOpenGLPixelFormatAttribute attrs[] = {
            NSOpenGLPFAAccelerated, NSOpenGLPFAClosestPolicy,
            NSOpenGLPFAAllowOfflineRenderers,
            (NSOpenGLPixelFormatAttribute)kCGLPFASupportsAutomaticGraphicsSwitching,
            NSOpenGLPFAOpenGLProfile, NSOpenGLProfileVersion3_2Core,
            NSOpenGLPFAColorSize, 24, NSOpenGLPFAAlphaSize, 8,
            NSOpenGLPFADepthSize, 0, NSOpenGLPFAStencilSize, 0,
            NSOpenGLPFADoubleBuffer, NSOpenGLPFASampleBuffers, 0, 0
        };
        NSOpenGLPixelFormat *format = [[NSOpenGLPixelFormat alloc] initWithAttributes:attrs];
        if (!format) {
            report[@"failure"] = @"No pixel format for Kitty's accelerated core-profile attributes";
            return emit(report, 1);
        }
        report[@"pixel_format_created"] = @YES;
        NSOpenGLContext *context = [[NSOpenGLContext alloc] initWithFormat:format shareContext:nil];
        if (!context) {
            report[@"failure"] = @"Native OpenGL context creation failed";
            return emit(report, 1);
        }
        report[@"context_created"] = @YES;
        [context makeCurrentContext];
        GLint accelerated = 0, major = 0, minor = 0, profile = 0;
        [format getValues:&accelerated forAttribute:NSOpenGLPFAAccelerated
            forVirtualScreen:context.currentVirtualScreen];
        glGetIntegerv(GL_MAJOR_VERSION, &major);
        glGetIntegerv(GL_MINOR_VERSION, &minor);
        glGetIntegerv(GL_CONTEXT_PROFILE_MASK, &profile);
        report[@"gl_version"] = glText(GL_VERSION);
        report[@"gl_renderer"] = glText(GL_RENDERER);
        report[@"gl_vendor"] = glText(GL_VENDOR);
        report[@"accelerated"] = @(accelerated);
        report[@"major"] = @(major); report[@"minor"] = @(minor);
        report[@"core_profile"] = @((profile & GL_CONTEXT_CORE_PROFILE_BIT) != 0);
        GLenum error = glGetError();
        report[@"gl_error"] = @(error);
        BOOL capable = error == GL_NO_ERROR && accelerated != 0 &&
            (major > 3 || (major == 3 && minor >= 3)) &&
            (profile & GL_CONTEXT_CORE_PROFILE_BIT) != 0 &&
            [report[@"gl_version"] length] > 0 && [report[@"gl_renderer"] length] > 0;
        report[@"capable"] = @(capable);
        [NSOpenGLContext clearCurrentContext];
        if (!capable) report[@"failure"] = @"Context does not meet stock Kitty requirements";
        return emit(report, capable ? 0 : 1);
    }
}
