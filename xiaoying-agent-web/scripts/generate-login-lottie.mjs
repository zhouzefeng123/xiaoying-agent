import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const projectRoot = path.resolve(scriptDirectory, '..');
const outputDirectory = path.join(projectRoot, 'src', 'assets', 'lottie');
const outputFile = path.join(outputDirectory, 'xiaoying-robot.json');

const FRAMES = 180;
const FRAME_RATE = 30;

const palette = {
    skin: '#F5CC55',
    skinLight: '#FFF2A6',
    skinMid: '#FFE077',
    skinShade: '#D7A53B',
    skinLine: '#8A6324',
    hair: '#E2B043',
    hairDark: '#79551E',
    hairLight: '#FFF0A2',
    cloth: '#FFF8DF',
    clothShade: '#F0E1B4',
    clothLine: '#CDB978',
    accent: '#95C45A',
    accentDeep: '#5D8B35',
    blush: '#F2AFA9',
    white: '#FFFFFF',
    eye: '#4E8A38',
    eyeDeep: '#25491E',
    shadow: '#5A4B27',
};

const toColor = (hex) => {
    const normalized = hex.replace('#', '');
    return [
        Number.parseInt(normalized.slice(0, 2), 16) / 255,
        Number.parseInt(normalized.slice(2, 4), 16) / 255,
        Number.parseInt(normalized.slice(4, 6), 16) / 255,
        1,
    ];
};

const fixed = (value, index) => ({ a: 0, k: value, ix: index });
const vector = (value, index) => ({ a: 0, k: value, ix: index });

function animated(entries, index) {
    return {
        a: 1,
        k: entries.map((entry, entryIndex) => {
            const nextEntry = entries[entryIndex + 1];
            const keyframe = {
                t: entry.t,
                s: entry.s,
            };
            if (nextEntry) {
                keyframe.e = nextEntry.s;
                keyframe.i = { x: [0.35], y: [1] };
                keyframe.o = { x: [0.65], y: [0] };
            }
            return keyframe;
        }),
        ix: index,
    };
}

function transform({
    x = 0,
    y = 0,
    rotation = 0,
    scale = [100, 100],
    opacity = 100,
} = {}) {
    return {
        ty: 'tr',
        p: vector([x, y], 2),
        a: vector([0, 0], 1),
        s: vector(scale, 3),
        r: typeof rotation === 'number' ? fixed(rotation, 6) : rotation,
        o: typeof opacity === 'number' ? fixed(opacity, 7) : opacity,
        sk: fixed(0, 4),
        sa: fixed(0, 5),
        nm: 'Transform',
    };
}

function group(name, children, options = {}) {
    const item = {
        ty: 'gr',
        nm: name,
        np: children.length + 1,
        cix: 2,
        it: [...children, transform(options)],
        hd: false,
    };
    if (options.className) {
        item.cl = options.className;
    }
    return item;
}

function solidFill(hex, opacity = 100) {
    return {
        ty: 'fl',
        c: fixed(toColor(hex), 4),
        o: fixed(opacity, 5),
        r: 1,
        nm: 'Fill',
        mn: 'ADBE Vector Graphic - Fill',
        hd: false,
    };
}

function gradientFill(colors, {
    start,
    end,
    radial = false,
    opacity = 100,
} = {}) {
    const stops = colors.flatMap(([offset, hex]) => {
        const [red, green, blue] = toColor(hex);
        return [offset, red, green, blue];
    });

    return {
        ty: 'gf',
        o: fixed(opacity, 5),
        r: 1,
        bm: 0,
        g: {
            p: colors.length,
            k: fixed(stops, 9),
        },
        s: vector(start, 1),
        e: vector(end, 2),
        t: radial ? 2 : 1,
        nm: 'Gradient fill',
        mn: 'ADBE Vector Graphic - G-Fill',
        hd: false,
    };
}

function stroke(hex, width = 3, opacity = 100) {
    return {
        ty: 'st',
        c: fixed(toColor(hex), 3),
        o: fixed(opacity, 4),
        w: fixed(width, 5),
        lc: 2,
        lj: 2,
        ml: 4,
        nm: 'Stroke',
        mn: 'ADBE Vector Graphic - Stroke',
        hd: false,
    };
}

function ellipse(name, {
    x = 0,
    y = 0,
    width,
    height,
    fillColor,
    fillGradient,
    fillOpacity = 100,
    strokeColor,
    strokeWidth = 2,
    strokeOpacity = 100,
    rotation = 0,
    opacity = 100,
    className,
}) {
    const children = [
        {
            ty: 'el',
            d: 1,
            s: vector([width, height], 2),
            p: vector([0, 0], 3),
            nm: `${name} Path`,
            mn: 'ADBE Vector Shape - Ellipse',
            hd: false,
        },
    ];
    if (fillGradient) {
        children.push(fillGradient);
    } else if (fillColor) {
        children.push(solidFill(fillColor, fillOpacity));
    }
    if (strokeColor) {
        children.push(stroke(strokeColor, strokeWidth, strokeOpacity));
    }
    return group(name, children, { x, y, rotation, opacity, className });
}

function rect(name, {
    x = 0,
    y = 0,
    width,
    height,
    radius = 0,
    fillColor,
    fillGradient,
    fillOpacity = 100,
    strokeColor,
    strokeWidth = 2,
    strokeOpacity = 100,
    rotation = 0,
    opacity = 100,
    className,
}) {
    const children = [
        {
            ty: 'rc',
            d: 1,
            s: vector([width, height], 2),
            p: vector([0, 0], 3),
            r: fixed(radius, 4),
            nm: `${name} Path`,
            mn: 'ADBE Vector Shape - Rect',
            hd: false,
        },
    ];
    if (fillGradient) {
        children.push(fillGradient);
    } else if (fillColor) {
        children.push(solidFill(fillColor, fillOpacity));
    }
    if (strokeColor) {
        children.push(stroke(strokeColor, strokeWidth, strokeOpacity));
    }
    return group(name, children, { x, y, rotation, opacity, className });
}

function polygon(name, points, {
    fillColor,
    fillGradient,
    fillOpacity = 100,
    strokeColor,
    strokeWidth = 3,
    strokeOpacity = 100,
    rotation = 0,
    opacity = 100,
    x = 0,
    y = 0,
    className,
} = {}) {
    const children = [
        {
            ty: 'sh',
            ks: fixed({
                i: points.map(() => [0, 0]),
                o: points.map(() => [0, 0]),
                v: points,
                c: true,
            }, 2),
            nm: `${name} Path`,
            mn: 'ADBE Vector Shape - Group',
            hd: false,
        },
    ];
    if (fillGradient) {
        children.push(fillGradient);
    } else if (fillColor) {
        children.push(solidFill(fillColor, fillOpacity));
    }
    if (strokeColor) {
        children.push(stroke(strokeColor, strokeWidth, strokeOpacity));
    }
    return group(name, children, { x, y, rotation, opacity, className });
}

function polyline(name, points, {
    color = palette.hairDark,
    width = 4,
    opacity = 100,
    x = 0,
    y = 0,
} = {}) {
    return group(name, [
        {
            ty: 'sh',
            ks: fixed({
                i: points.map(() => [0, 0]),
                o: points.map(() => [0, 0]),
                v: points,
                c: false,
            }, 2),
            nm: `${name} Path`,
            mn: 'ADBE Vector Shape - Group',
            hd: false,
        },
        stroke(color, width, opacity),
    ], { x, y });
}

function layer({
    index,
    name,
    shapes,
    position = [360, 360],
    anchor = [0, 0],
    rotation = 0,
    scale = [100, 100, 100],
    opacity = 100,
}) {
    return {
        ddd: 0,
        ind: index,
        ty: 4,
        nm: name,
        sr: 1,
        ks: {
            o: typeof opacity === 'number' ? fixed(opacity, 11) : opacity,
            r: typeof rotation === 'number' ? fixed(rotation, 10) : rotation,
            p: Array.isArray(position) ? vector([...position, 0], 2) : position,
            a: vector([...anchor, 0], 1),
            s: Array.isArray(scale) ? vector([...scale, 100].slice(0, 3), 6) : scale,
        },
        ao: 0,
        shapes,
        ip: 0,
        op: FRAMES,
        st: 0,
        bm: 0,
    };
}

const babySkin = (start, end) => gradientFill([
    [0, '#FFF9BE'],
    [0.36, '#FFE477'],
    [0.72, '#F2C241'],
    [1, '#C58C2E'],
], { start, end });

const babyCloth = (start, end) => gradientFill([
    [0, '#FFFEFA'],
    [0.52, '#FFF9E9'],
    [1, '#F0E4C9'],
], { start, end });

const babyEye = (x) => gradientFill([
    [0, '#E4FFB8'],
    [0.28, '#9DD65B'],
    [0.64, '#5B9C38'],
    [1, '#244E20'],
], { start: [x, -98], end: [x, -48], radial: true });

const babySclera = (x) => gradientFill([
    [0, '#FFFFFF'],
    [0.62, '#FFFDF2'],
    [1, '#F0E8CE'],
], { start: [x, -100], end: [x, -48], radial: true });

const headElements = [
    ...[-1, 1].flatMap((direction) => [
        polygon(`Dragon horn ${direction}`, [
            [direction * 68, -224],
            [direction * 96, -292],
            [direction * 148, -220],
            [direction * 118, -198],
        ], {
            fillGradient: gradientFill([
                [0, '#FFF4B0'],
                [0.5, '#F5CF63'],
                [1, '#C58B2C'],
            ], {
                start: [direction * 68, -292],
                end: [direction * 148, -198],
            }),
            strokeColor: palette.skinLine,
            strokeWidth: 3,
        }),
        polyline(`Horn ridge ${direction}`, [
            [direction * 92, -268],
            [direction * 112, -234],
        ], {
            color: palette.skinLine,
            width: 2,
            opacity: 42,
        }),
    ]),
    ellipse('Nailong head', {
        x: 0,
        y: -92,
        width: 402,
        height: 306,
        fillGradient: babySkin([-201, -245], [201, 61]),
        strokeColor: palette.skinLine,
        strokeWidth: 4,
    }),
    ellipse('Nailong top sheen', {
        x: -38,
        y: -216,
        width: 194,
        height: 64,
        fillColor: palette.skinLight,
        fillOpacity: 30,
        rotation: -8,
    }),
    ellipse('Nailong eye shadow left', {
        x: -78,
        y: -98,
        width: 128,
        height: 116,
        fillColor: palette.skinShade,
        fillOpacity: 20,
    }),
    ellipse('Nailong eye shadow right', {
        x: 78,
        y: -98,
        width: 128,
        height: 116,
        fillColor: palette.skinShade,
        fillOpacity: 20,
    }),
    ellipse('Sclera left', {
        x: -78,
        y: -98,
        width: 118,
        height: 104,
        fillGradient: babySclera(-78),
        strokeColor: palette.skinLine,
        strokeWidth: 2,
    }),
    ellipse('Sclera right', {
        x: 78,
        y: -98,
        width: 118,
        height: 104,
        fillGradient: babySclera(78),
        strokeColor: palette.skinLine,
        strokeWidth: 2,
    }),
    group('Eye look', [
        ...[-1, 1].flatMap((direction) => {
            const centerX = direction * 78;
            return [
                ellipse(`Iris ${direction}`, {
                    x: centerX,
                    y: -98,
                    width: 78,
                    height: 84,
                    fillGradient: babyEye(centerX),
                }),
                ellipse(`Iris inner glow ${direction}`, {
                    x: centerX,
                    y: -98,
                    width: 48,
                    height: 54,
                    strokeColor: '#A9D8B8',
                    strokeWidth: 1.5,
                    strokeOpacity: 45,
                }),
                ellipse(`Pupil ${direction}`, {
                    x: centerX,
                    y: -98,
                    width: 34,
                    height: 42,
                    fillColor: '#102925',
                }),
                ellipse(`Catchlight ${direction}`, {
                    x: centerX - 15,
                    y: -122,
                    width: 21,
                    height: 21,
                    fillColor: palette.white,
                    fillOpacity: 96,
                }),
                ellipse(`Catchlight small ${direction}`, {
                    x: centerX + 18,
                    y: -82,
                    width: 8,
                    height: 8,
                    fillColor: palette.white,
                    fillOpacity: 82,
                }),
            ];
        }),
    ].reverse(), { className: 'robot-lottie-look' }),
    ...[-1, 1].map((direction) => ellipse(`Nailong nostril ${direction}`, {
        x: direction * 11,
        y: -49,
        width: 6,
        height: 8,
        fillColor: palette.skinLine,
        fillOpacity: 72,
    })),
    ellipse('Dragon mouth', {
        x: 0,
        y: -10,
        width: 148,
        height: 66,
        fillColor: '#7B3E35',
        strokeColor: palette.skinLine,
        strokeWidth: 3,
    }),
    ellipse('Tongue', {
        x: 0,
        y: 11,
        width: 62,
        height: 24,
        fillColor: '#ED9A92',
        fillOpacity: 78,
    }),
    ...[-1, 1].map((direction) => rect(`Dragon tooth ${direction}`, {
        x: direction * 20,
        y: -31,
        width: 25,
        height: 16,
        radius: 6,
        fillColor: '#FFFDF6',
        strokeColor: '#B98B66',
        strokeWidth: 1.2,
    })),
    ellipse('Smile cheek left', {
        x: -79,
        y: -17,
        width: 18,
        height: 9,
        fillColor: palette.skinLight,
        fillOpacity: 20,
    }),
    ellipse('Smile cheek right', {
        x: 79,
        y: -17,
        width: 18,
        height: 9,
        fillColor: palette.skinLight,
        fillOpacity: 20,
    }),
    ...[-1, 1].map((direction) => ellipse(`Nailong blush ${direction}`, {
        x: direction * 142,
        y: -38,
        width: 52,
        height: 24,
        fillColor: palette.blush,
        fillOpacity: 28,
    })),
];

const headLook = group(
    'Head follow',
    [...headElements].reverse(),
    { className: 'robot-lottie-head-follow' },
);

const neckUpper = group('Soft neck upper', [
    rect('Upper neck', {
        x: 0,
        y: -5,
        width: 48,
        height: 58,
        radius: 23,
        fillGradient: babySkin([-24, -34], [24, 24]),
        strokeColor: palette.skinShade,
        strokeWidth: 2,
    }),
    ellipse('Neck crease upper', {
        x: 0,
        y: -24,
        width: 64,
        height: 18,
        strokeColor: palette.skinShade,
        strokeWidth: 2,
        strokeOpacity: 28,
    }),
], { className: 'robot-lottie-neck-upper' });

const neckMid = group('Soft neck middle', [
    rect('Middle neck', {
        x: 0,
        y: 35,
        width: 60,
        height: 50,
        radius: 24,
        fillGradient: babySkin([-30, 9], [30, 61]),
        strokeColor: palette.skinShade,
        strokeWidth: 2,
    }),
    ellipse('Soft neck fold', {
        x: 0,
        y: 56,
        width: 92,
        height: 26,
        fillGradient: babySkin([-46, 43], [46, 69]),
        strokeColor: palette.skinShade,
        strokeWidth: 2,
    }),
    neckUpper,
], { className: 'robot-lottie-neck-mid' });

const neckLower = group('Soft neck lower', [
    rect('Lower neck', {
        x: 0,
        y: 74,
        width: 76,
        height: 50,
        radius: 25,
        fillGradient: babySkin([-38, 48], [38, 100]),
        strokeColor: palette.skinShade,
        strokeWidth: 2,
    }),
    ellipse('Soft neck base', {
        x: 0,
        y: 102,
        width: 152,
        height: 34,
        fillGradient: babySkin([-76, 85], [76, 119]),
        strokeColor: palette.skinShade,
        strokeWidth: 3,
    }),
    neckMid,
], { className: 'robot-lottie-neck-lower' });

const torso = group('Nailong body', [
    ...[-1, 1].map((direction) => polygon(`Dragon wing ${direction}`, [
        [direction * 104, 142],
        [direction * 198, 111],
        [direction * 190, 184],
        [direction * 143, 211],
    ], {
        fillGradient: gradientFill([
            [0, '#FFF4B3'],
            [0.5, '#F7D467'],
            [1, '#DCA33A'],
        ], {
            start: [direction * 104, 111],
            end: [direction * 198, 211],
        }),
        strokeColor: palette.skinLine,
        strokeWidth: 2.5,
    })),
    neckLower,
    ellipse('Dragon body', {
        x: 0,
        y: 199,
        width: 244,
        height: 264,
        fillGradient: babySkin([-122, 68], [122, 331]),
        strokeColor: palette.skinLine,
        strokeWidth: 3.5,
    }),
    ellipse('Cream belly', {
        x: 0,
        y: 205,
        width: 176,
        height: 184,
        fillGradient: babyCloth([-88, 113], [88, 297]),
        strokeColor: '#D8C79F',
        strokeWidth: 2,
    }),
    ellipse('Left dragon foot', {
        x: -58,
        y: 327,
        width: 82,
        height: 40,
        fillGradient: babySkin([-99, 307], [-17, 347]),
        strokeColor: palette.skinLine,
        strokeWidth: 2.5,
    }),
    ellipse('Right dragon foot', {
        x: 58,
        y: 327,
        width: 82,
        height: 40,
        fillGradient: babySkin([17, 307], [99, 347]),
        strokeColor: palette.skinLine,
        strokeWidth: 2.5,
    }),
    ellipse('Left toe pad', {
        x: -58,
        y: 334,
        width: 30,
        height: 13,
        fillColor: palette.skinLight,
        fillOpacity: 24,
    }),
    ellipse('Right toe pad', {
        x: 58,
        y: 334,
        width: 30,
        height: 13,
        fillColor: palette.skinLight,
        fillOpacity: 24,
    }),
].reverse(), { x: 0, y: 0 });

const sparks = [
    layer({
        index: 1,
        name: 'Bubble one',
        position: [126, 255],
        opacity: animated([
            { t: 0, s: [50] },
            { t: 42, s: [95] },
            { t: 92, s: [52] },
            { t: 142, s: [92] },
            { t: 179, s: [50] },
        ], 11),
        shapes: [
            ellipse('Spark dot', {
                width: 13,
                height: 13,
                fillColor: palette.accent,
            }),
            polyline('Spark cross top', [[0, -25], [0, -17]], {
                color: palette.accentDeep,
                width: 3.5,
                opacity: 68,
            }),
            polyline('Spark cross bottom', [[0, 17], [0, 25]], {
                color: palette.accentDeep,
                width: 3.5,
                opacity: 68,
            }),
            polyline('Spark cross left', [[-25, 0], [-17, 0]], {
                color: palette.accentDeep,
                width: 3.5,
                opacity: 68,
            }),
            polyline('Spark cross right', [[17, 0], [25, 0]], {
                color: palette.accentDeep,
                width: 3.5,
                opacity: 68,
            }),
        ],
    }),
    layer({
        index: 2,
        name: 'Bubble two',
        position: [590, 225],
        opacity: animated([
            { t: 0, s: [72] },
            { t: 54, s: [32] },
            { t: 108, s: [88] },
            { t: 150, s: [44] },
            { t: 179, s: [72] },
        ], 11),
        shapes: [
            ellipse('Milk ring', {
                width: 23,
                height: 23,
                strokeColor: '#B9CFC8',
                strokeWidth: 3,
            }),
            ellipse('Milk ring core', {
                width: 7,
                height: 7,
                fillColor: '#C8DDD7',
            }),
        ],
    }),
    layer({
        index: 3,
        name: 'Bubble three',
        position: [574, 484],
        rotation: animated([
            { t: 0, s: [12] },
            { t: 90, s: [28] },
            { t: 179, s: [12] },
        ], 10),
        opacity: animated([
            { t: 0, s: [34] },
            { t: 70, s: [68] },
            { t: 132, s: [38] },
            { t: 179, s: [34] },
        ], 11),
        shapes: [
            ellipse('Bubble ring', {
                width: 23,
                height: 23,
                strokeColor: '#A8CDB0',
                strokeWidth: 2.5,
            }),
        ],
    }),
];

const head = layer({
    index: 4,
    name: 'Head',
    position: animated([
        { t: 0, s: [360, 335, 0] },
        { t: 45, s: [360, 331, 0] },
        { t: 90, s: [360, 335, 0] },
        { t: 135, s: [360, 331, 0] },
        { t: 179, s: [360, 335, 0] },
    ], 2),
    rotation: animated([
        { t: 0, s: [-0.5] },
        { t: 45, s: [0.5] },
        { t: 90, s: [-0.5] },
        { t: 135, s: [0.5] },
        { t: 179, s: [-0.5] },
    ], 10),
    shapes: [headLook],
});

const rightArm = layer({
    index: 5,
    name: 'Right dragon arm',
    position: [470, 460],
    shapes: [
        rect('Right dragon arm', {
            x: 12,
            y: 40,
            width: 48,
            height: 88,
            radius: 24,
            fillGradient: babySkin([-12, -4], [36, 84]),
            strokeColor: palette.skinLine,
            strokeWidth: 2.5,
            rotation: 6,
        }),
        ellipse('Right elbow crease', {
            x: 34,
            y: 82,
            width: 38,
            height: 38,
            fillColor: palette.skinShade,
            fillOpacity: 35,
        }),
        rect('Right wrist crease', {
            x: 47,
            y: 137,
            width: 44,
            height: 13,
            radius: 6,
            fillGradient: babySkin([25, 131], [69, 143]),
            strokeColor: palette.skinLine,
            strokeWidth: 1.5,
            rotation: 3,
        }),
        ellipse('Right hand', {
            x: 55,
            y: 175,
            width: 45,
            height: 51,
            fillGradient: babySkin([32, 150], [78, 201]),
            strokeColor: palette.skinLine,
            strokeWidth: 2,
        }),
        polyline('Right palm crease', [
            [42, 173],
            [64, 177],
        ], { color: palette.skinShade, width: 2, opacity: 48 }),
        ellipse('Right thumb', {
            x: 74,
            y: 167,
            width: 17,
            height: 22,
            fillGradient: babySkin([65, 156], [83, 178]),
            strokeColor: palette.skinLine,
            strokeWidth: 1.5,
            rotation: 24,
        }),
    ].reverse(),
});

const leftArm = layer({
    index: 6,
    name: 'Left dragon arm',
    position: [250, 460],
    shapes: [
        rect('Left dragon arm', {
            x: -12,
            y: 40,
            width: 48,
            height: 88,
            radius: 24,
            fillGradient: babySkin([-36, -4], [12, 84]),
            strokeColor: palette.skinLine,
            strokeWidth: 2.5,
            rotation: -6,
        }),
        ellipse('Left elbow crease', {
            x: -34,
            y: 82,
            width: 38,
            height: 38,
            fillColor: palette.skinShade,
            fillOpacity: 35,
        }),
        rect('Left wrist crease', {
            x: -47,
            y: 137,
            width: 44,
            height: 13,
            radius: 6,
            fillGradient: babySkin([-69, 131], [-25, 143]),
            strokeColor: palette.skinLine,
            strokeWidth: 1.5,
            rotation: -3,
        }),
        ellipse('Left hand', {
            x: -55,
            y: 175,
            width: 45,
            height: 51,
            fillGradient: babySkin([-78, 150], [-32, 201]),
            strokeColor: palette.skinLine,
            strokeWidth: 2,
        }),
        polyline('Left palm crease', [
            [-64, 177],
            [-42, 173],
        ], { color: palette.skinShade, width: 2, opacity: 48 }),
        ellipse('Left thumb', {
            x: -74,
            y: 167,
            width: 17,
            height: 22,
            fillGradient: babySkin([-83, 156], [-65, 178]),
            strokeColor: palette.skinLine,
            strokeWidth: 1.5,
            rotation: -24,
        }),
    ].reverse(),
});

const body = layer({
    index: 7,
    name: 'Body',
    position: [360, 360],
    shapes: [torso],
});

const orbit = layer({
    index: 8,
    name: 'Orbit',
    position: [360, 350],
    rotation: animated([
        { t: 0, s: [0] },
        { t: 179, s: [360] },
    ], 10),
    opacity: 48,
    shapes: [
        ellipse('Orbit ellipse', {
            width: 574,
            height: 520,
            strokeColor: '#B8C9C2',
            strokeWidth: 2,
            rotation: -17,
        }),
        ellipse('Orbit ellipse inner', {
            width: 506,
            height: 470,
            strokeColor: '#E4C48C',
            strokeWidth: 2,
            opacity: 38,
            rotation: 23,
        }),
        ellipse('Orbit dot top', {
            x: 220,
            y: -208,
            width: 9,
            height: 9,
            fillColor: palette.accent,
        }),
        ellipse('Orbit dot right', {
            x: 262,
            y: 94,
            width: 8,
            height: 8,
            fillColor: '#92B8AF',
        }),
    ],
});

const shadow = layer({
    index: 9,
    name: 'Shadow',
    position: [360, 687],
    scale: animated([
        { t: 0, s: [100, 100, 100] },
        { t: 45, s: [95, 95, 100] },
        { t: 90, s: [100, 100, 100] },
        { t: 135, s: [95, 95, 100] },
        { t: 179, s: [100, 100, 100] },
    ], 6),
    shapes: [
        ellipse('Soft shadow', {
            width: 430,
            height: 50,
            fillColor: palette.shadow,
            fillOpacity: 14,
        }),
        ellipse('Contact shadow', {
            width: 288,
            height: 20,
            fillColor: palette.shadow,
            fillOpacity: 10,
        }),
    ],
});

const animation = {
    v: '5.13.0',
    fr: FRAME_RATE,
    ip: 0,
    op: FRAMES,
    w: 720,
    h: 720,
    nm: 'XiaoYing Nailong mascot',
    ddd: 0,
    assets: [],
    layers: [
        ...sparks,
        head,
        rightArm,
        leftArm,
        body,
        orbit,
        shadow,
    ],
    markers: [],
};

await mkdir(outputDirectory, { recursive: true });
await writeFile(outputFile, `${JSON.stringify(animation)}\n`, 'utf8');

console.log(`Generated ${path.relative(projectRoot, outputFile)} (${animation.layers.length} layers)`);
