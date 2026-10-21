import { useEffect, useRef, useState } from 'react';
import lottie, { type AnimationItem } from 'lottie-web/build/player/lottie_light';
import animationSource from '../../assets/lottie/xiaoying-robot.json?raw';

interface RobotLottieProps {
    className?: string;
}

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

export function RobotLottie({ className = '' }: RobotLottieProps) {
    const stageRef = useRef<HTMLDivElement>(null);
    const playerRef = useRef<HTMLDivElement>(null);
    const [isReady, setIsReady] = useState(false);
    const [reducedMotion, setReducedMotion] = useState(() => (
        typeof window !== 'undefined'
        && window.matchMedia('(prefers-reduced-motion: reduce)').matches
    ));

    useEffect(() => {
        if (typeof window === 'undefined') {
            return;
        }

        const mediaQuery = window.matchMedia('(prefers-reduced-motion: reduce)');
        const handleChange = () => setReducedMotion(mediaQuery.matches);
        mediaQuery.addEventListener('change', handleChange);
        return () => mediaQuery.removeEventListener('change', handleChange);
    }, []);

    useEffect(() => {
        const container = playerRef.current;
        if (!container) {
            return;
        }

        let disposed = false;
        let animation: AnimationItem | null = null;

        try {
            container.replaceChildren();
            animation = lottie.loadAnimation({
                container,
                renderer: 'svg',
                loop: true,
                autoplay: !reducedMotion,
                animationData: JSON.parse(animationSource),
                rendererSettings: {
                    progressiveLoad: true,
                    preserveAspectRatio: 'xMidYMid meet',
                    hideOnTransparent: true,
                    className: 'robot-lottie__vector',
                },
            });
            animation.setSpeed(0.78);
            if (reducedMotion) {
                animation.goToAndStop(0, true);
            }
            animation.addEventListener('DOMLoaded', () => {
                if (!disposed) {
                    setIsReady(true);
                }
            });
        } catch (error: unknown) {
            console.error('[RobotLottie] Failed to load mascot animation', error);
        }

        return () => {
            disposed = true;
            animation?.destroy();
        };
    }, [reducedMotion]);

    useEffect(() => {
        const stage = stageRef.current;
        if (!stage || reducedMotion) {
            return;
        }

        let animationFrame = 0;
        let nextLookX = 0;
        let nextLookY = 0;
        let typingTarget: HTMLInputElement | HTMLTextAreaElement | null = null;

        const applyLook = () => {
            animationFrame = 0;
            stage.style.setProperty('--robot-look-x', nextLookX.toFixed(3));
            stage.style.setProperty('--robot-look-y', nextLookY.toFixed(3));
        };

        const updateLookFromPoint = (clientX: number, clientY: number) => {
            const rect = stage.getBoundingClientRect();
            if (rect.width === 0 || rect.height === 0) {
                return;
            }

            const headCenterX = rect.left + rect.width * 0.5;
            const headCenterY = rect.top + rect.height * 0.45;
            const horizontalReach = Math.max(window.innerWidth * 0.48, 320);
            const verticalReach = Math.max(window.innerHeight * 0.58, 280);

            nextLookX = clamp((clientX - headCenterX) / horizontalReach, -1, 1);
            nextLookY = clamp((clientY - headCenterY) / verticalReach, -1, 1);

            if (!animationFrame) {
                animationFrame = window.requestAnimationFrame(applyLook);
            }
        };

        const handlePointerMove = (event: PointerEvent) => {
            if (typingTarget && document.activeElement === typingTarget) {
                return;
            }
            updateLookFromPoint(event.clientX, event.clientY);
        };

        const isSensitiveInput = (target: HTMLInputElement | HTMLTextAreaElement) => {
            if (target instanceof HTMLInputElement && target.type === 'password') {
                return true;
            }
            const fieldDescription = [
                target.id,
                target.getAttribute('name') ?? '',
                target.getAttribute('autocomplete') ?? '',
            ].join(' ');
            return /password|passcode|verification|one-time-code|otp|code/i.test(fieldDescription);
        };

        const handleFocusIn = (event: FocusEvent) => {
            const target = event.target;
            if (!(target instanceof HTMLInputElement) && !(target instanceof HTMLTextAreaElement)) {
                return;
            }

            typingTarget = target;
            const rect = target.getBoundingClientRect();
            if (isSensitiveInput(target)) {
                const stageRect = stage.getBoundingClientRect();
                updateLookFromPoint(
                    stageRect.left - stageRect.width * 0.28,
                    stageRect.top + stageRect.height * 0.52,
                );
            } else {
                updateLookFromPoint(
                    rect.left + rect.width * 0.56,
                    rect.top + rect.height * 0.5,
                );
            }
        };

        const handleFocusOut = (event: FocusEvent) => {
            if (event.target === typingTarget) {
                typingTarget = null;
            }
        };

        const resetLook = () => {
            if (typingTarget && document.activeElement === typingTarget) {
                return;
            }
            nextLookX = 0;
            nextLookY = 0;
            if (!animationFrame) {
                animationFrame = window.requestAnimationFrame(applyLook);
            }
        };

        window.addEventListener('pointermove', handlePointerMove, { passive: true });
        window.addEventListener('blur', resetLook);
        document.addEventListener('focusin', handleFocusIn);
        document.addEventListener('focusout', handleFocusOut);
        document.documentElement.addEventListener('pointerleave', resetLook);

        return () => {
            window.removeEventListener('pointermove', handlePointerMove);
            window.removeEventListener('blur', resetLook);
            document.removeEventListener('focusin', handleFocusIn);
            document.removeEventListener('focusout', handleFocusOut);
            document.documentElement.removeEventListener('pointerleave', resetLook);
            if (animationFrame) {
                window.cancelAnimationFrame(animationFrame);
            }
            stage.style.removeProperty('--robot-look-x');
            stage.style.removeProperty('--robot-look-y');
        };
    }, [reducedMotion]);

    return (
        <div
            ref={stageRef}
            className={`robot-lottie ${className}`}
            role="img"
            aria-label="抱着手臂、会跟随视线的奶蛙助手"
        >
            <div
                ref={playerRef}
                className={`robot-lottie__player${isReady ? ' robot-lottie__player--ready' : ''}`}
                aria-hidden="true"
            />
        </div>
    );
}
