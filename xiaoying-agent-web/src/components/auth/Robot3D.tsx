import { useEffect, useRef, useState } from 'react';
import {
    ACESFilmicToneMapping,
    CanvasTexture,
    CapsuleGeometry,
    Color,
    DirectionalLight,
    Group,
    HemisphereLight,
    MathUtils,
    Mesh,
    MeshBasicMaterial,
    MeshPhysicalMaterial,
    PCFSoftShadowMap,
    PerspectiveCamera,
    PlaneGeometry,
    PMREMGenerator,
    Scene,
    SphereGeometry,
    SRGBColorSpace,
    ShadowMaterial,
    TorusGeometry,
    WebGLRenderer,
} from 'three';
import { RoomEnvironment } from 'three/examples/jsm/environments/RoomEnvironment.js';

interface Robot3DProps {
    className?: string;
}

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

export function Robot3D({ className = '' }: Robot3DProps) {
    const stageRef = useRef<HTMLDivElement>(null);
    const mountRef = useRef<HTMLDivElement>(null);
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
            const headCenterY = rect.top + rect.height * 0.38;
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

    useEffect(() => {
        const mount = mountRef.current;
        if (!mount) {
            return;
        }

        mount.replaceChildren();
        let renderer: WebGLRenderer;
        try {
            renderer = new WebGLRenderer({
                antialias: true,
                alpha: true,
                powerPreference: 'high-performance',
            });
        } catch (error: unknown) {
            console.error('[Robot3D] WebGL is unavailable', error);
            return;
        }

        renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
        renderer.setClearColor(0x000000, 0);
        renderer.outputColorSpace = SRGBColorSpace;
        renderer.toneMapping = ACESFilmicToneMapping;
        renderer.toneMappingExposure = 1.16;
        renderer.shadowMap.enabled = true;
        renderer.shadowMap.type = PCFSoftShadowMap;
        renderer.domElement.className = 'robot-3d__canvas';
        mount.appendChild(renderer.domElement);

        const scene = new Scene();
        const camera = new PerspectiveCamera(27, 1, 0.1, 40);
        camera.position.set(0, 0.52, 9.2);
        camera.lookAt(0, 0.18, 0);

        const environment = new RoomEnvironment();
        const pmrem = new PMREMGenerator(renderer);
        const environmentTexture = pmrem.fromScene(environment, 0.04).texture;
        scene.environment = environmentTexture;
        environment.dispose();
        pmrem.dispose();

        const skin = new MeshPhysicalMaterial({
            color: new Color('#f3c84f'),
            roughness: 0.46,
            metalness: 0,
            clearcoat: 0.52,
            clearcoatRoughness: 0.3,
            sheen: 0.28,
            sheenColor: new Color('#fff0a1'),
            envMapIntensity: 0.72,
        });
        const skinShade = new MeshPhysicalMaterial({
            color: new Color('#ddb03f'),
            roughness: 0.52,
            clearcoat: 0.36,
            clearcoatRoughness: 0.38,
            envMapIntensity: 0.6,
        });
        const bellyMaterial = new MeshPhysicalMaterial({
            color: new Color('#fff3d3'),
            roughness: 0.62,
            clearcoat: 0.18,
            clearcoatRoughness: 0.48,
            envMapIntensity: 0.5,
        });
        const whiteMaterial = new MeshPhysicalMaterial({
            color: new Color('#fff9e8'),
            roughness: 0.32,
            clearcoat: 0.5,
            clearcoatRoughness: 0.2,
        });
        const irisMaterial = new MeshPhysicalMaterial({
            color: new Color('#79a858'),
            roughness: 0.25,
            clearcoat: 0.62,
            clearcoatRoughness: 0.18,
            emissive: new Color('#1d3718'),
            emissiveIntensity: 0.08,
        });
        const pupilMaterial = new MeshPhysicalMaterial({
            color: new Color('#102218'),
            roughness: 0.2,
            clearcoat: 0.72,
            clearcoatRoughness: 0.12,
        });
        const mouthMaterial = new MeshPhysicalMaterial({
            color: new Color('#693d2f'),
            roughness: 0.5,
            clearcoat: 0.22,
        });
        const tongueMaterial = new MeshPhysicalMaterial({
            color: new Color('#e99889'),
            roughness: 0.5,
            clearcoat: 0.24,
        });
        const blushMaterial = new MeshPhysicalMaterial({
            color: new Color('#ef9f87'),
            roughness: 0.68,
            transparent: true,
            opacity: 0.5,
        });
        const detailMaterial = new MeshPhysicalMaterial({
            color: new Color('#7a5427'),
            roughness: 0.56,
        });

        const sphereGeometry = new SphereGeometry(1, 64, 48);
        const smallSphereGeometry = new SphereGeometry(1, 32, 24);
        const armGeometry = new CapsuleGeometry(0.23, 0.68, 12, 28);
        const neckGeometry = new CapsuleGeometry(0.33, 0.42, 12, 32);
        const mouthGeometry = new TorusGeometry(0.24, 0.035, 10, 48, Math.PI);

        const createMesh = (
            geometry: SphereGeometry | CapsuleGeometry | TorusGeometry,
            material: MeshPhysicalMaterial,
            position: [number, number, number],
            scale: [number, number, number] = [1, 1, 1],
            rotation: [number, number, number] = [0, 0, 0],
        ) => {
            const mesh = new Mesh(geometry, material);
            mesh.position.set(...position);
            mesh.scale.set(...scale);
            mesh.rotation.set(...rotation);
            mesh.castShadow = true;
            mesh.receiveShadow = true;
            return mesh;
        };

        const character = new Group();
        character.position.y = 0.02;
        scene.add(character);

        const bodyRoot = new Group();
        character.add(bodyRoot);

        const body = createMesh(
            sphereGeometry,
            skin,
            [0, -0.42, 0],
            [1.12, 1.36, 0.92],
        );
        bodyRoot.add(body);

        const belly = createMesh(
            sphereGeometry,
            bellyMaterial,
            [0, -0.5, 0.72],
            [0.72, 0.94, 0.24],
        );
        bodyRoot.add(belly);

        const footLeft = createMesh(
            sphereGeometry,
            skinShade,
            [-0.5, -1.58, 0.26],
            [0.53, 0.24, 0.67],
            [0, -0.08, -0.05],
        );
        const footRight = footLeft.clone();
        footRight.position.x = 0.5;
        footRight.rotation.y = 0.08;
        bodyRoot.add(footLeft, footRight);

        const leftArm = createMesh(
            armGeometry,
            skin,
            [-1.02, -0.34, 0.18],
            [1, 1, 1],
            [0.06, 0.06, 0.38],
        );
        const rightArm = createMesh(
            armGeometry,
            skin,
            [1.02, -0.34, 0.18],
            [1, 1, 1],
            [0.06, -0.06, -0.38],
        );
        bodyRoot.add(leftArm, rightArm);

        const leftHand = createMesh(
            smallSphereGeometry,
            skin,
            [-1.2, -0.78, 0.39],
            [0.29, 0.25, 0.24],
        );
        const rightHand = createMesh(
            smallSphereGeometry,
            skin,
            [1.2, -0.78, 0.39],
            [0.29, 0.25, 0.24],
        );
        bodyRoot.add(leftHand, rightHand);

        const wingLeft = createMesh(
            sphereGeometry,
            skinShade,
            [-1.08, -0.08, -0.48],
            [0.34, 0.7, 0.16],
            [0.05, 0.18, -0.48],
        );
        const wingRight = wingLeft.clone();
        wingRight.position.x = 1.08;
        wingRight.rotation.set(0.05, -0.18, 0.48);
        bodyRoot.add(wingLeft, wingRight);

        const headPivot = new Group();
        headPivot.position.set(0, 0.45, 0);
        character.add(headPivot);

        const neck = createMesh(
            neckGeometry,
            skinShade,
            [0, 0.3, 0],
            [1, 1, 0.95],
        );
        headPivot.add(neck);

        const head = createMesh(
            sphereGeometry,
            skin,
            [0, 1.02, 0.02],
            [1.29, 0.94, 0.89],
        );
        headPivot.add(head);

        const earLeft = createMesh(
            smallSphereGeometry,
            skinShade,
            [-0.92, 1.52, -0.04],
            [0.27, 0.38, 0.18],
            [-0.1, 0.18, -0.25],
        );
        const earRight = earLeft.clone();
        earRight.position.x = 0.92;
        earRight.rotation.set(-0.1, -0.18, 0.25);
        headPivot.add(earLeft, earRight);

        const eyeCenters = [-0.43, 0.43];
        const lookGroups: Group[] = [];

        eyeCenters.forEach((x) => {
            const eyeRoot = new Group();
            eyeRoot.position.set(x, 1.1, 0.78);
            headPivot.add(eyeRoot);

            const sclera = createMesh(
                smallSphereGeometry,
                whiteMaterial,
                [0, 0, 0],
                [0.34, 0.41, 0.13],
            );
            eyeRoot.add(sclera);

            const look = new Group();
            look.position.z = 0.14;
            eyeRoot.add(look);
            lookGroups.push(look);

            const iris = createMesh(
                smallSphereGeometry,
                irisMaterial,
                [0, 0, 0],
                [0.205, 0.27, 0.07],
            );
            look.add(iris);

            const pupil = createMesh(
                smallSphereGeometry,
                pupilMaterial,
                [0, -0.015, 0.063],
                [0.108, 0.16, 0.035],
            );
            look.add(pupil);

            const catchlightMain = createMesh(
                smallSphereGeometry,
                whiteMaterial,
                [-0.07, 0.105, 0.096],
                [0.055, 0.065, 0.024],
            );
            const catchlightSmall = createMesh(
                smallSphereGeometry,
                whiteMaterial,
                [0.075, -0.07, 0.093],
                [0.025, 0.029, 0.014],
            );
            look.add(catchlightMain, catchlightSmall);
        });

        const noseLeft = createMesh(
            smallSphereGeometry,
            detailMaterial,
            [-0.1, 0.9, 0.87],
            [0.036, 0.048, 0.022],
        );
        const noseRight = noseLeft.clone();
        noseRight.position.x = 0.1;
        headPivot.add(noseLeft, noseRight);

        const mouth = createMesh(
            mouthGeometry,
            mouthMaterial,
            [0, 0.72, 0.86],
            [1, 0.84, 1],
            [0, 0, Math.PI],
        );
        const mouthInterior = createMesh(
            smallSphereGeometry,
            mouthMaterial,
            [0, 0.72, 0.83],
            [0.23, 0.1, 0.025],
            [0, 0, 0],
        );
        headPivot.add(mouthInterior, mouth);

        const tongue = createMesh(
            smallSphereGeometry,
            tongueMaterial,
            [0, 0.66, 0.875],
            [0.13, 0.045, 0.025],
        );
        headPivot.add(tongue);

        const toothLeft = createMesh(
            smallSphereGeometry,
            whiteMaterial,
            [-0.075, 0.75, 0.885],
            [0.062, 0.045, 0.022],
        );
        const toothRight = toothLeft.clone();
        toothRight.position.x = 0.075;
        headPivot.add(toothLeft, toothRight);

        const blushLeft = createMesh(
            smallSphereGeometry,
            blushMaterial,
            [-0.82, 0.74, 0.7],
            [0.22, 0.09, 0.035],
        );
        const blushRight = blushLeft.clone();
        blushRight.position.x = 0.82;
        headPivot.add(blushLeft, blushRight);

        const keyLight = new DirectionalLight(0xfff3d7, 3.4);
        keyLight.position.set(-4.5, 6.5, 5.5);
        keyLight.castShadow = true;
        keyLight.shadow.mapSize.set(1024, 1024);
        keyLight.shadow.camera.left = -5;
        keyLight.shadow.camera.right = 5;
        keyLight.shadow.camera.top = 6;
        keyLight.shadow.camera.bottom = -5;
        keyLight.shadow.camera.near = 0.5;
        keyLight.shadow.camera.far = 18;
        keyLight.shadow.bias = -0.0008;
        keyLight.shadow.radius = 6;
        scene.add(keyLight);

        const fillLight = new DirectionalLight(0xbfe5d5, 1.55);
        fillLight.position.set(5.5, 2.5, 4);
        scene.add(fillLight);

        const rimLight = new DirectionalLight(0xffe8a5, 2.4);
        rimLight.position.set(-1.5, 4, -5);
        scene.add(rimLight);

        const hemisphere = new HemisphereLight(0xf7fff1, 0x85653b, 1.75);
        scene.add(hemisphere);

        const shadowCanvas = document.createElement('canvas');
        shadowCanvas.width = 128;
        shadowCanvas.height = 128;
        const shadowContext = shadowCanvas.getContext('2d');
        if (shadowContext) {
            const gradient = shadowContext.createRadialGradient(64, 64, 4, 64, 64, 60);
            gradient.addColorStop(0, 'rgba(49, 45, 31, 0.32)');
            gradient.addColorStop(0.42, 'rgba(49, 45, 31, 0.14)');
            gradient.addColorStop(1, 'rgba(49, 45, 31, 0)');
            shadowContext.fillStyle = gradient;
            shadowContext.fillRect(0, 0, 128, 128);
        }
        const contactTexture = new CanvasTexture(shadowCanvas);
        const contactShadow = new Mesh(
            new PlaneGeometry(3.7, 1.7),
            new MeshBasicMaterial({
                map: contactTexture,
                transparent: true,
                depthWrite: false,
                toneMapped: false,
            }),
        );
        contactShadow.rotation.x = -Math.PI / 2;
        contactShadow.position.set(0, -1.79, 0.08);
        scene.add(contactShadow);

        const shadowFloor = new Mesh(
            new PlaneGeometry(12, 12),
            new ShadowMaterial({ color: 0x443b26, opacity: 0.16 }),
        );
        shadowFloor.rotation.x = -Math.PI / 2;
        shadowFloor.position.y = -1.81;
        shadowFloor.receiveShadow = true;
        scene.add(shadowFloor);

        const ringMaterial = new MeshBasicMaterial({
            color: new Color('#a5cba7'),
            transparent: true,
            opacity: 0.2,
            depthWrite: false,
            toneMapped: false,
        });
        const orbitOne = new Mesh(new TorusGeometry(1.72, 0.012, 8, 128), ringMaterial);
        orbitOne.position.set(0, 0.22, -0.34);
        orbitOne.rotation.set(1.12, 0.18, -0.2);
        const orbitTwo = new Mesh(new TorusGeometry(1.5, 0.009, 8, 128), ringMaterial.clone());
        orbitTwo.material.opacity = 0.13;
        orbitTwo.position.set(0, 0.16, -0.4);
        orbitTwo.rotation.set(1.32, -0.22, 0.36);
        scene.add(orbitOne, orbitTwo);

        let width = Math.max(mount.clientWidth, 1);
        let height = Math.max(mount.clientHeight, 1);
        const resize = () => {
            width = Math.max(mount.clientWidth, 1);
            height = Math.max(mount.clientHeight, 1);
            renderer.setSize(width, height, false);
            camera.aspect = width / height;
            camera.updateProjectionMatrix();
            renderer.render(scene, camera);
        };
        const resizeObserver = new ResizeObserver(resize);
        resizeObserver.observe(mount);
        resize();

        let frame = 0;
        let previousTime = performance.now();
        const baseHeadY = headPivot.position.y;
        const baseBodyY = bodyRoot.position.y;

        const animate = (time: number) => {
            const delta = Math.min((time - previousTime) / 1000, 0.05);
            previousTime = time;

            if (reducedMotion) {
                renderer.render(scene, camera);
                return;
            }

            const stageElement = stageRef.current;
            const lookX = Number.parseFloat(stageElement?.style.getPropertyValue('--robot-look-x') ?? '') || 0;
            const lookY = Number.parseFloat(stageElement?.style.getPropertyValue('--robot-look-y') ?? '') || 0;
            const elapsed = time * 0.001;

            headPivot.rotation.y = MathUtils.damp(headPivot.rotation.y, -lookX * 0.17, 5, delta);
            headPivot.rotation.x = MathUtils.damp(headPivot.rotation.x, lookY * 0.075, 5, delta);
            headPivot.rotation.z = MathUtils.damp(
                headPivot.rotation.z,
                -lookX * MathUtils.degToRad(5.2) + Math.sin(elapsed * 0.72) * 0.012,
                5,
                delta,
            );
            headPivot.position.y = baseHeadY + Math.sin(elapsed * 1.4) * 0.018;

            lookGroups.forEach((look) => {
                look.position.x = MathUtils.damp(look.position.x, lookX * 0.085, 9, delta);
                look.position.y = MathUtils.damp(look.position.y, -lookY * 0.06, 9, delta);
            });

            const breath = Math.sin(elapsed * 1.4);
            bodyRoot.position.y = baseBodyY + breath * 0.012;
            bodyRoot.scale.set(1 + breath * 0.0025, 1 + breath * 0.005, 1 + breath * 0.002);

            orbitOne.rotation.z += delta * 0.08;
            orbitTwo.rotation.z -= delta * 0.055;
            renderer.render(scene, camera);
            frame = window.requestAnimationFrame(animate);
        };

        const readyFrame = window.requestAnimationFrame(() => setIsReady(true));
        frame = window.requestAnimationFrame(animate);

        return () => {
            window.cancelAnimationFrame(readyFrame);
            window.cancelAnimationFrame(frame);
            resizeObserver.disconnect();

            const geometries = new Set<SphereGeometry | CapsuleGeometry | TorusGeometry | PlaneGeometry>();
            const materials = new Set<MeshPhysicalMaterial | MeshBasicMaterial | ShadowMaterial>();
            scene.traverse((object) => {
                if (!(object instanceof Mesh)) {
                    return;
                }
                geometries.add(object.geometry as SphereGeometry | CapsuleGeometry | TorusGeometry | PlaneGeometry);
                if (Array.isArray(object.material)) {
                    object.material.forEach((material) => materials.add(material as MeshPhysicalMaterial | MeshBasicMaterial | ShadowMaterial));
                } else {
                    materials.add(object.material as MeshPhysicalMaterial | MeshBasicMaterial | ShadowMaterial);
                }
            });
            geometries.forEach((geometry) => geometry.dispose());
            materials.forEach((material) => material.dispose());
            contactTexture.dispose();
            environmentTexture.dispose();
            renderer.dispose();
            renderer.domElement.remove();
        };
    }, [reducedMotion]);

    return (
        <div
            ref={stageRef}
            className={`robot-3d${isReady ? ' robot-3d--ready' : ''} ${className}`.trim()}
            role="img"
            aria-label="跟随视线转头的三维奶龙助手"
        >
            <div ref={mountRef} className="robot-3d__mount" aria-hidden="true" />
            {!isReady && <span className="robot-3d__loading" aria-hidden="true" />}
        </div>
    );
}
