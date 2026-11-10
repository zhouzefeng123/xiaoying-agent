import { useEffect, useRef } from 'react';
import './AnimatedCharacters.css';

// Adapted from arsh342/careercompass's animated-characters concept and geometry.
// Source and MIT license: public/licenses/careercompass.txt.
interface AnimatedCharactersProps {
    focusedField: 'email' | 'code' | null;
    isLoading: boolean;
}

const clamp = (value: number, limit: number) => Math.max(-limit, Math.min(limit, value));

// Keep both feet planted while the top bends; matching commands allow CSS path interpolation.
function pillarPath(width: number, height: number, bend: number) {
    return `M0 ${height} C0 ${height * 0.65} ${bend} ${height * 0.55} ${bend} 4 Q${bend} 0 ${bend + 4} 0 H${width + bend - 4} Q${width + bend} 0 ${width + bend} 4 C${width + bend} ${height * 0.55} ${width} ${height * 0.65} ${width} ${height} Z`;
}

function yellowPath(bend: number, nod: number) {
    // The round head follows the gaze while curved sides keep the feet in place.
    return `M0 175 C0 125 ${bend} ${90 + nod} ${bend} ${55 + nod} C${bend} ${24.625 + nod} ${24.625 + bend} ${nod} ${55 + bend} ${nod} C${85.375 + bend} ${nod} ${110 + bend} ${24.625 + nod} ${110 + bend} ${55 + nod} C${110 + bend} ${90 + nod} 110 125 110 175 Z`;
}

function orangePath(bend: number, nod: number) {
    // Two cubic curves preserve the original dome at rest and let its crown flex.
    return `M0 133 C0 44.5 ${65 + bend} ${0.25 + nod} ${130 + bend} ${0.25 + nod} C${195 + bend} ${0.25 + nod} 260 44.5 260 133 Z`;
}

function Eyes({ x, y, dark = false }: { x: number; y: number; dark?: boolean }) {
    return (
        <g transform={`translate(${x} ${y})`}>
            <g className="login-character-eyes">
                {[0, 36].map(offset => (
                    <g key={offset} transform={`translate(${offset} 0)`}>
                        {!dark && <circle r="9" fill="#fff" />}
                        <circle className="login-character-pupil" r={dark ? 5.5 : 4} fill="#252529" />
                    </g>
                ))}
            </g>
        </g>
    );
}

export function AnimatedCharacters({ focusedField, isLoading }: AnimatedCharactersProps) {
    const sceneRef = useRef<SVGSVGElement>(null);

    useEffect(() => {
        const scene = sceneRef.current;
        if (!scene) return;
        const media = window.matchMedia('(prefers-reduced-motion: reduce)');
        const characters = Array.from(scene.querySelectorAll<SVGGElement>('[data-character]'));
        const bodies = characters.map(character => character.querySelector<SVGPathElement>('[data-body]'));
        const yellowEye = scene.querySelector<SVGCircleElement>('[data-character="yellow"] .login-character-pupil');
        const yellowMouth = scene.querySelector<SVGPathElement>('[data-character="yellow"] .login-character-smile');
        let point: { x: number; y: number } | null = null;
        let frame = 0;
        let sharingGlance = focusedField === 'email';

        const update = () => {
            frame = 0;
            const field = focusedField === 'email' ? document.getElementById('login-email') : null;
            const fieldRect = field?.getBoundingClientRect();
            const target = fieldRect ? { x: fieldRect.left + fieldRect.width / 2, y: fieldRect.top + fieldRect.height / 2 } : point;
            // Use fixed stage coordinates so bending/turning cannot move the gaze threshold.
            const stage = scene.getBoundingClientRect();
            const anchors = [[202.5, 108], [284, 186], [155, 274], [365, 240]];
            characters.forEach((character, index) => {
                const dx = target ? target.x - (stage.left + anchors[index][0] * stage.width / 500) : 0;
                const dy = target ? target.y - (stage.top + anchors[index][1] * stage.height / 420) : 0;
                const away = focusedField === 'code';
                const still = media.matches;
                const isYellow = character.dataset.character === 'yellow';
                const isOrange = character.dataset.character === 'orange';
                const glance = sharingGlance && index < 2 && !still;
                const angle = Math.atan2(dy, dx);
                const distance = target ? Math.min(Math.hypot(dx, dy) / 40, 4) : 0;
                // Turn continuously through a front-facing pose instead of flipping at the center.
                const turn = still || (!target && !away) ? 1 : away ? -1 : clamp(dx / Math.max(stage.width * 0.24, 60), 1);
                character.style.setProperty('--body-width', `${still || (!target && !away) ? 1 : 1 - Math.abs(turn) * 0.08}`);
                character.style.setProperty('--face-x', `${isYellow ? 0 : isOrange ? turn * 32 : still ? 0 : away ? -14 : clamp(dx / 28, 12)}px`);
                if (isYellow) {
                    yellowEye?.style.setProperty('cx', `${55 + turn * 6}px`);
                    yellowMouth?.style.setProperty('d', `path("M${32 + turn * 28} 62h46")`);
                }
                character.style.setProperty('--face-y', `${still ? 0 : away ? -3 : clamp(dy / 40, 8)}px`);
                character.style.setProperty('--pupil-x', `${still ? 0 : away ? -4 : glance ? (index === 0 ? 4 : -4) : Math.cos(angle) * distance}px`);
                character.style.setProperty('--pupil-y', `${still ? 0 : away ? -2 : glance ? (index === 0 ? 3 : -3) : Math.sin(angle) * distance}px`);
                const bendLimit = isOrange ? 32 : isYellow ? 34 : focusedField === 'email' ? 42 : 30;
                const bend = still ? 0 : away ? -24 : glance ? (index === 0 ? 38 : -18) : clamp(dx / 15, bendLimit);
                const nod = still || !(isYellow || isOrange) ? 0 : away ? -4 : clamp(dy / 65, 7);
                character.style.setProperty('--head-offset', `${isOrange ? bend * 0.7 : bend}px`);
                character.style.setProperty('--head-rise', `${isOrange ? nod * 0.7 : nod}px`);

                const body = bodies[index];
                if (body) {
                    const width = Number(body.dataset.width);
                    const height = Number(body.dataset.height);
                    const path = isYellow ? yellowPath(bend, nod) : isOrange ? orangePath(bend, nod) : pillarPath(width, height, bend);
                    body.style.setProperty('d', `path("${path}")`);
                }
            });
        };
        const scheduleUpdate = () => {
            if (!frame) frame = window.requestAnimationFrame(update);
        };
        const onPointerMove = (event: PointerEvent) => {
            if (event.pointerType === 'touch' || media.matches) return;
            point = { x: event.clientX, y: event.clientY };
            scheduleUpdate();
        };
        const reset = () => { point = null; scheduleUpdate(); };
        scheduleUpdate();
        const glanceTimer = focusedField === 'email' ? window.setTimeout(() => {
            sharingGlance = false;
            scheduleUpdate();
        }, 850) : undefined;
        window.addEventListener('pointermove', onPointerMove, { passive: true });
        window.addEventListener('blur', reset);
        window.addEventListener('resize', scheduleUpdate);
        document.documentElement.addEventListener('pointerleave', reset);
        media.addEventListener('change', scheduleUpdate);
        return () => {
            window.cancelAnimationFrame(frame);
            window.clearTimeout(glanceTimer);
            window.removeEventListener('pointermove', onPointerMove);
            window.removeEventListener('blur', reset);
            window.removeEventListener('resize', scheduleUpdate);
            document.documentElement.removeEventListener('pointerleave', reset);
            media.removeEventListener('change', scheduleUpdate);
        };
    }, [focusedField]);

    return (
        <svg ref={sceneRef} viewBox="0 0 500 420" className={`login-characters${isLoading ? ' login-characters--loading' : ''}`} role="img" aria-label="会跟随鼠标、眨眼的紫色、黑色、橙色和黄色小伙伴">
            <g transform="translate(125 40)">
                <g data-character="purple" className="login-character login-character--purple">
                    <g className="login-character-entrance">
                        <g className="login-character-breathe">
                            <path data-body data-width="155" data-height="340" d={pillarPath(155, 340, 0)} fill="#7529FF" />
                            <g className="login-character-face">
                                <Eyes x={48} y={35} />
                                <path d="M61 58q6 7 12 0" className="login-character-smile" stroke="#fff" />
                            </g>
                        </g>
                    </g>
                </g>
            </g>
            <g transform="translate(237 138)">
                <g data-character="black" className="login-character login-character--black">
                    <g className="login-character-entrance">
                        <g className="login-character-breathe">
                            <path data-body data-width="94" data-height="242" d={pillarPath(94, 242, 0)} fill="#252529" />
                            <g className="login-character-face"><Eyes x={25} y={24} /></g>
                        </g>
                    </g>
                </g>
            </g>
            <g transform="translate(25 247)">
                <g data-character="orange" className="login-character login-character--orange">
                    <g className="login-character-entrance">
                        <g className="login-character-breathe">
                            <g className="login-character-turn">
                                <path data-body d={orangePath(0, 0)} fill="#FF8303" />
                                <g className="login-character-face">
                                    <Eyes x={112} y={54} dark />
                                    <path d="M121 72q9 13 18 0Z" fill="#252529" />
                                </g>
                            </g>
                        </g>
                    </g>
                </g>
            </g>
            <g transform="translate(310 205)">
                <g data-character="yellow" className="login-character login-character--yellow">
                    <g className="login-character-entrance">
                        <g className="login-character-breathe">
                            <g className="login-character-turn">
                                <path data-body d={yellowPath(0, 0)} fill="#EAD600" />
                                <g className="login-character-face">
                                    <g className="login-character-eyes"><circle className="login-character-pupil" cx="61" cy="40" r="5" fill="#252529" /></g>
                                    <path d="M60 62h46" className="login-character-smile" stroke="#252529" />
                                </g>
                            </g>
                        </g>
                    </g>
                </g>
            </g>
        </svg>
    );
}
