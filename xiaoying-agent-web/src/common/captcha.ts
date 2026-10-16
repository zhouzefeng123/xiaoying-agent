export interface CaptchaVerifyResp {
    captchaResult: boolean;
    bizResult: boolean;
}

export const RegistryCaptcha = (
    buttonID: string,
    elementID: string,
    captchaVerifyCallback: (captchaVerifyParam: string) => Promise<CaptchaVerifyResp>,
    getInstance: (instance: unknown) => void,
    onBizResultCallback?: (bizResultParam: unknown) => void,
) => {
    type AliyunCaptchaOptions = {
        SceneId: string
        prefix: string
        mode: string
        element: string
        button: string
        captchaVerifyCallback: (captchaVerifyParam: string) => Promise<CaptchaVerifyResp>
        onBizResultCallback?: (bizResultParam: unknown) => void
        getInstance: (instance: unknown) => void
        slideStyle: {
            width: number
            height: number
        }
        language: string
    }

    const initAliyunCaptcha = (window as unknown as { initAliyunCaptcha?: (opts: AliyunCaptchaOptions) => void }).initAliyunCaptcha
    if (!initAliyunCaptcha) {
        console.error('[Captcha] Aliyun Captcha SDK not loaded');
        return
    }

    initAliyunCaptcha({
        SceneId: '10fgfcwd', // 场景ID
        prefix: '1kwucu', // 身份标
        mode: 'popup', // 验证码模式
        element: '#' + elementID, // 渲染验证码的元素 (需要加#号)
        button: '#' + buttonID, // 触发验证码弹窗的元素 (需要加#号)
        captchaVerifyCallback: captchaVerifyCallback, // 业务请求(带验证码校验)回调函数
        onBizResultCallback: onBizResultCallback, // 业务请求结果回调函数
        getInstance: getInstance, // 绑定验证码实例函数
        slideStyle: {
            width: 360,
            height: 40,
        },
        language: 'cn', // 验证码语言类型
    });
}

export const UnmountCaptcha = () => {
    document.getElementById('aliyunCaptcha-mask')?.remove();
    document.getElementById('aliyunCaptcha-window-popup')?.remove();
}
