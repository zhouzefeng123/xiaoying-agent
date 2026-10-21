import { useId } from 'react';

const bodySilhouette = 'M360 65 C301 63 269 96 261 155 C249 242 229 282 181 319 C126 356 109 407 128 476 C107 525 108 594 135 646 C167 704 246 716 360 716 C471 716 550 704 582 646 C611 594 610 525 592 476 C611 407 593 356 539 319 C490 282 471 242 459 155 C451 96 418 63 360 65Z';
const backArm = 'M174 342 C140 364 119 407 127 448 C133 493 159 520 204 521 C271 520 375 463 466 416 C480 409 478 395 464 386 C446 374 422 376 401 385 L252 435 C229 442 217 436 215 416 C215 390 214 357 196 344 C189 338 181 338 174 342Z';
const frontArm = 'M542 341 C565 349 584 378 592 413 C602 455 584 490 556 505 C527 522 492 514 456 500 C384 474 328 434 258 427 C244 425 236 417 241 406 C249 388 276 381 300 384 C363 388 420 405 470 419 C494 426 508 420 508 400 C505 377 508 351 525 343 C531 340 537 339 542 341Z';
const rightHand = 'M421 386 C439 372 455 354 478 351 C491 347 506 347 511 352 C515 359 502 362 484 364 C501 360 523 359 526 367 C529 376 511 377 488 379 C507 377 526 380 526 387 C526 397 505 395 484 397 C504 397 520 400 517 407 C512 416 481 414 460 410Z';
const leftHand = 'M293 393 C269 378 242 375 220 383 C201 388 190 402 189 418 C210 424 234 425 254 433 L291 434Z';

/** A toy-like mascot with separate body and neck layers for believable parallax. */
export function MilkFrogArtwork() {
    const id = useId().replace(/:/g, '');
    const paint = (name: string) => `url(#${id}-${name})`;

    return (
        <svg viewBox="0 0 720 760" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true" focusable="false">
            <defs>
                <radialGradient id={`${id}-skin`} cx="30%" cy="14%" r="94%" fx="24%" fy="9%">
                    <stop stopColor="#FFFBDA" />
                    <stop offset=".18" stopColor="#FFF18D" />
                    <stop offset=".52" stopColor="#F4D35E" />
                    <stop offset=".8" stopColor="#D6AA42" />
                    <stop offset="1" stopColor="#AE7829" />
                </radialGradient>
                <linearGradient id={`${id}-skin-depth`} x1="150" y1="120" x2="610" y2="690" gradientUnits="userSpaceOnUse">
                    <stop offset=".28" stopColor="#9B691F" stopOpacity="0" />
                    <stop offset=".68" stopColor="#8E5A17" stopOpacity=".04" />
                    <stop offset="1" stopColor="#66400F" stopOpacity=".3" />
                </linearGradient>
                <radialGradient id={`${id}-light`}>
                    <stop stopColor="#FFFDE5" stopOpacity=".95" />
                    <stop offset=".46" stopColor="#FFF7C3" stopOpacity=".5" />
                    <stop offset="1" stopColor="#FFF7C3" stopOpacity="0" />
                </radialGradient>
                <radialGradient id={`${id}-shade`}>
                    <stop stopColor="#674414" stopOpacity=".52" />
                    <stop offset=".58" stopColor="#7D5318" stopOpacity=".22" />
                    <stop offset="1" stopColor="#98712F" stopOpacity="0" />
                </radialGradient>
                <radialGradient id={`${id}-belly`} cx="39%" cy="21%" r="73%" fx="32%" fy="12%">
                    <stop stopColor="#FFFDF2" />
                    <stop offset=".38" stopColor="#F8EDCA" />
                    <stop offset=".76" stopColor="#E2CE99" />
                    <stop offset="1" stopColor="#B99D64" />
                </radialGradient>
                <linearGradient id={`${id}-belly-edge`} x1="200" y1="370" x2="535" y2="720" gradientUnits="userSpaceOnUse">
                    <stop stopColor="#FFF8DE" stopOpacity=".78" />
                    <stop offset=".55" stopColor="#B49B68" stopOpacity=".28" />
                    <stop offset="1" stopColor="#70501E" stopOpacity=".5" />
                </linearGradient>
                <radialGradient id={`${id}-arm-back`} cx="20%" cy="10%" r="92%" fx="16%" fy="6%">
                    <stop stopColor="#FFFBCC" />
                    <stop offset=".34" stopColor="#F5DE71" />
                    <stop offset=".72" stopColor="#D6AF47" />
                    <stop offset="1" stopColor="#9D6C25" />
                </radialGradient>
                <radialGradient id={`${id}-arm-front`} cx="42%" cy="9%" r="94%" fx="34%" fy="4%">
                    <stop stopColor="#FFF9B9" />
                    <stop offset=".31" stopColor="#F1D563" />
                    <stop offset=".68" stopColor="#CDA33C" />
                    <stop offset="1" stopColor="#8F5E1D" />
                </radialGradient>
                <linearGradient id={`${id}-hand`} x1="250" y1="350" x2="510" y2="445" gradientUnits="userSpaceOnUse">
                    <stop stopColor="#D7CA8A" />
                    <stop offset=".32" stopColor="#AB9858" />
                    <stop offset=".7" stopColor="#76623A" />
                    <stop offset="1" stopColor="#42371F" />
                </linearGradient>
                <radialGradient id={`${id}-socket`}>
                    <stop stopColor="#6E4B19" stopOpacity=".55" />
                    <stop offset=".7" stopColor="#8F6A27" stopOpacity=".18" />
                    <stop offset="1" stopColor="#A47B30" stopOpacity="0" />
                </radialGradient>
                <radialGradient id={`${id}-iris`} cx="34%" cy="20%" r="82%" fx="28%" fy="14%">
                    <stop stopColor="#D5E9AE" />
                    <stop offset=".3" stopColor="#AFC982" />
                    <stop offset=".7" stopColor="#799554" />
                    <stop offset="1" stopColor="#455F35" />
                </radialGradient>
                <radialGradient id={`${id}-pupil`} cx="38%" cy="19%" r="82%" fx="32%" fy="14%">
                    <stop stopColor="#2F4A31" />
                    <stop offset=".5" stopColor="#122419" />
                    <stop offset="1" stopColor="#07120B" />
                </radialGradient>
                <radialGradient id={`${id}-cheek`}>
                    <stop stopColor="#F2A373" stopOpacity=".44" />
                    <stop offset="1" stopColor="#E99865" stopOpacity="0" />
                </radialGradient>
                <linearGradient id={`${id}-mouth`} x1="360" y1="205" x2="360" y2="249" gradientUnits="userSpaceOnUse">
                    <stop stopColor="#402512" />
                    <stop offset=".58" stopColor="#6F3D22" />
                    <stop offset="1" stopColor="#9C6947" />
                </linearGradient>
                <linearGradient id={`${id}-lip`} x1="323" y1="0" x2="397" y2="0" gradientUnits="userSpaceOnUse">
                    <stop stopColor="#E6B24F" />
                    <stop offset=".5" stopColor="#FFF2AC" />
                    <stop offset="1" stopColor="#D89B35" />
                </linearGradient>
                <radialGradient id={`${id}-ground`}>
                    <stop stopColor="#3F4C35" stopOpacity=".34" />
                    <stop offset=".48" stopColor="#596246" stopOpacity=".18" />
                    <stop offset="1" stopColor="#6F7351" stopOpacity="0" />
                </radialGradient>
                <pattern id={`${id}-grain`} width="27" height="27" patternUnits="userSpaceOnUse">
                    <circle cx="5" cy="8" r="1.1" fill="#664514" opacity=".16" />
                    <circle cx="21" cy="18" r=".8" fill="#FFF6BE" opacity=".26" />
                    <circle cx="12" cy="24" r=".65" fill="#664514" opacity=".12" />
                </pattern>
                <filter id={`${id}-soft`} x="-35%" y="-35%" width="170%" height="170%">
                    <feGaussianBlur stdDeviation="13" />
                </filter>
                <filter id={`${id}-feather`} x="-40%" y="-40%" width="180%" height="180%">
                    <feGaussianBlur stdDeviation="5" />
                </filter>
                <filter id={`${id}-crease`} x="-45%" y="-90%" width="190%" height="280%">
                    <feGaussianBlur stdDeviation="3" />
                </filter>
                <filter id={`${id}-arm-shadow`} x="-30%" y="-30%" width="160%" height="180%" colorInterpolationFilters="sRGB">
                    <feDropShadow dx="0" dy="12" stdDeviation="9" floodColor="#634713" floodOpacity=".38" />
                </filter>
                <filter id={`${id}-eye-shadow`} x="-50%" y="-40%" width="200%" height="190%" colorInterpolationFilters="sRGB">
                    <feDropShadow dx="0" dy="3" stdDeviation="2.4" floodColor="#6B4A16" floodOpacity=".34" />
                </filter>
                <clipPath id={`${id}-body-clip`}><path d={bodySilhouette} /></clipPath>
                <linearGradient id={`${id}-neck-fade`} x1="0" y1="72" x2="0" y2="348" gradientUnits="userSpaceOnUse">
                    <stop offset="0" stopColor="#fff" />
                    <stop offset=".7" stopColor="#fff" />
                    <stop offset="1" stopColor="#fff" stopOpacity="0" />
                </linearGradient>
                <linearGradient id={`${id}-body-fade`} x1="0" y1="226" x2="0" y2="348" gradientUnits="userSpaceOnUse">
                    <stop offset="0" stopColor="#fff" stopOpacity="0" />
                    <stop offset=".38" stopColor="#fff" stopOpacity=".24" />
                    <stop offset="1" stopColor="#fff" />
                </linearGradient>
                <mask id={`${id}-neck-mask`} maskUnits="userSpaceOnUse" x="0" y="0" width="720" height="390">
                    <rect width="720" height="390" fill={paint('neck-fade')} />
                </mask>
                <mask id={`${id}-body-mask`} maskUnits="userSpaceOnUse" x="0" y="0" width="720" height="730">
                    <rect width="720" height="730" fill={paint('body-fade')} />
                </mask>
            </defs>

            {/* The floor shadow grows softer with distance to seat the character in the scene. */}
            <ellipse cx="360" cy="718" rx="286" ry="40" fill={paint('ground')} filter={paint('soft')} />
            <ellipse cx="360" cy="707" rx="166" ry="19" fill="#413722" opacity=".26" filter={paint('feather')} />
            <ellipse cx="307" cy="699" rx="91" ry="10" fill="#FFF8CF" opacity=".18" filter={paint('feather')} />

            <g className="milk-frog-breathe">
                {/* The head edge is hidden softly at the neck; the lower body never tilts. */}
                <path d={bodySilhouette} fill={paint('skin')} mask={paint('body-mask')} />
                <g clipPath={paint('body-clip')}>
                    <path d={bodySilhouette} fill={paint('skin-depth')} />
                    <ellipse cx="350" cy="548" rx="224" ry="211" fill={paint('belly')} filter={paint('soft')} />
                    <ellipse cx="320" cy="486" rx="131" ry="151" fill={paint('light')} opacity=".35" />
                    <ellipse cx="393" cy="636" rx="162" ry="112" fill={paint('shade')} opacity=".24" filter={paint('soft')} />
                    <ellipse cx="350" cy="548" rx="223" ry="210" stroke={paint('belly-edge')} strokeWidth="5" opacity=".75" />
                    <path d="M204 497 C229 616 276 682 353 699" stroke="#FFF8D9" strokeWidth="8" strokeLinecap="round" opacity=".2" filter={paint('feather')} />
                    <ellipse cx="575" cy="520" rx="48" ry="191" fill={paint('shade')} opacity=".3" />
                    <ellipse cx="355" cy="696" rx="193" ry="43" fill={paint('shade')} opacity=".42" />
                    <path d={bodySilhouette} fill={paint('grain')} opacity=".5" />
                    <path d="M270 112 C286 81 318 69 354 70" stroke="#FFFDDD" strokeWidth="15" strokeLinecap="round" opacity=".48" filter={paint('feather')} />
                    <path d="M466 156 C474 225 499 272 538 311" stroke="#FFF1AC" strokeWidth="9" strokeLinecap="round" opacity=".2" filter={paint('feather')} />
                </g>

                <ellipse cx="360" cy="326" rx="108" ry="28" fill="#78511B" opacity=".14" filter={paint('soft')} />

                <g className="robot-lottie-neck-follow">
                    <g mask={paint('neck-mask')}>
                        <path d={bodySilhouette} fill={paint('skin')} />
                        <g clipPath={paint('body-clip')}>
                            <path d={bodySilhouette} fill={paint('skin-depth')} />
                            <ellipse cx="286" cy="137" rx="139" ry="103" fill={paint('light')} />
                            <ellipse cx="378" cy="207" rx="111" ry="133" fill={paint('shade')} opacity=".34" />
                            <path d={bodySilhouette} fill={paint('grain')} opacity=".38" />
                            <path d="M271 134 C281 84 309 72 354 73" stroke="#FFFCDD" strokeWidth="12" strokeLinecap="round" opacity=".46" filter={paint('feather')} />
                            <ellipse cx="360" cy="286" rx="102" ry="22" fill={paint('shade')} opacity=".46" filter={paint('feather')} />
                        </g>
                    </g>

                    <g className="milk-frog-face">
                        <ellipse cx="268" cy="190" rx="35" ry="23" fill={paint('cheek')} />
                        <ellipse cx="452" cy="190" rx="35" ry="23" fill={paint('cheek')} />

                        {[298, 422].map((x) => (
                            <g key={x}>
                                <ellipse cx={x + 1} cy="158" rx="37" ry="43" fill={paint('socket')} filter={paint('feather')} />
                                <g className="milk-frog-eye">
                                    <ellipse cx={x} cy={154} rx="26" ry="32" fill={paint('iris')} stroke="#5C703F" strokeWidth="2.2" filter={paint('eye-shadow')} />
                                    <ellipse cx={x + 7} cy="166" rx="13" ry="17" fill="#4E673A" opacity=".18" />
                                    <path d={`M${x - 21} 143 Q${x - 15} 119 ${x + 7} 125`} stroke="#EFF7C7" strokeWidth="2.4" strokeLinecap="round" opacity=".48" />
                                    <path d={`M${x - 18} 174 Q${x - 2} 187 ${x + 18} 172`} stroke="#BBD18C" strokeWidth="2.2" strokeLinecap="round" opacity=".3" />
                                    <g className="robot-lottie-look">
                                        <ellipse cx={x + 1} cy="160" rx="14.5" ry="20" fill={paint('pupil')} />
                                        <ellipse cx={x - 4} cy="149" rx="5" ry="3.4" fill="#F1F8DE" opacity=".38" transform={`rotate(-30 ${x - 4} 149)`} />
                                        <circle cx={x + 7} cy="171" r="2" fill="#EAF3D2" opacity=".2" />
                                    </g>
                                    <path d={`M${x - 23} 137 Q${x - 2} 124 ${x + 22} 139`} stroke="#6B4C18" strokeWidth="3" strokeLinecap="round" opacity=".2" filter={paint('crease')} />
                                </g>
                            </g>
                        ))}

                        <ellipse cx="360" cy="196" rx="12" ry="7" fill="#76511D" opacity=".12" filter={paint('crease')} />
                        <ellipse cx="349" cy="196" rx="2.7" ry="4" fill="#79541D" opacity=".5" />
                        <ellipse cx="371" cy="196" rx="2.7" ry="4" fill="#79541D" opacity=".5" />

                        <ellipse cx="360" cy="226" rx="60" ry="34" fill="#6B4717" opacity=".2" filter={paint('soft')} />
                        <path d="M310 211 Q360 204 410 211 Q402 249 360 252 Q318 249 310 211Z" fill={paint('mouth')} stroke="#754A20" strokeWidth="2.4" />
                        <path d="M324 216 Q360 222 396 216" stroke={paint('lip')} strokeWidth="3" strokeLinecap="round" opacity=".7" />
                        <ellipse cx="360" cy="242" rx="20" ry="8" fill="#E39A80" opacity=".3" filter={paint('crease')} />
                        <path d="M316 211 Q360 219 404 211" stroke="#FFF2A9" strokeWidth="2.2" strokeLinecap="round" opacity=".38" />
                    </g>
                </g>

                {/* Shoulder creases sit behind the folded arms. */}
                <path d="M207 354 Q223 376 216 405 M511 353 Q496 374 503 400" stroke="#704A18" strokeWidth="6" strokeLinecap="round" opacity=".36" filter={paint('crease')} />
                <path d="M210 351 Q226 372 220 399 M508 351 Q494 372 499 396" stroke="#FFF4B4" strokeWidth="3" strokeLinecap="round" opacity=".25" />

                <path d={backArm} fill="#6B4915" opacity=".24" transform="translate(0 11)" filter={paint('soft')} />
                <g filter={paint('arm-shadow')}>
                    <path d={backArm} fill={paint('arm-back')} stroke="#81591F" strokeWidth="2.2" strokeOpacity=".42" />
                    <path d="M172 346 C142 369 126 407 132 447 C138 487 163 511 203 514 C276 512 372 461 456 417" stroke="#FFF7B5" strokeWidth="8" strokeLinecap="round" opacity=".26" filter={paint('feather')} />
                    <path d="M158 448 C169 477 183 495 205 506" stroke="#704A18" strokeWidth="6" strokeLinecap="round" opacity=".28" filter={paint('crease')} />
                    <ellipse cx="171" cy="407" rx="30" ry="54" fill={paint('light')} transform="rotate(16 171 407)" opacity=".52" />
                </g>

                <path d={rightHand} fill="#513A1C" opacity=".2" transform="translate(0 9)" filter={paint('feather')} />
                <path d={rightHand} fill={paint('hand')} stroke="#4E391C" strokeWidth="1.6" strokeOpacity=".35" />
                <path d="M478 355 Q496 350 505 354 M486 366 Q501 363 512 368" stroke="#E4D69A" strokeWidth="2.3" strokeLinecap="round" opacity=".34" />

                <path d={leftHand} fill="#513A1C" opacity=".22" transform="translate(0 9)" filter={paint('feather')} />
                <path d={leftHand} fill={paint('hand')} stroke="#4E391C" strokeWidth="1.6" strokeOpacity=".35" />
                <path d="M205 398 Q224 383 249 391 M198 409 Q218 397 244 403" stroke="#E4D69A" strokeWidth="2.4" strokeLinecap="round" opacity=".32" />

                <path d={frontArm} fill="#5B3F18" opacity=".24" transform="translate(0 13)" filter={paint('soft')} />
                <g filter={paint('arm-shadow')}>
                    <path d={frontArm} fill={paint('arm-front')} stroke="#815417" strokeWidth="2.4" strokeOpacity=".46" />
                    <path d="M544 348 C561 357 578 382 586 414 C595 452 581 481 555 497 C525 513 493 506 458 492" stroke="#FFF7B7" strokeWidth="9" strokeLinecap="round" opacity=".34" filter={paint('feather')} />
                    <path d="M547 478 C526 491 500 490 474 479" stroke="#704615" strokeWidth="7" strokeLinecap="round" opacity=".27" filter={paint('crease')} />
                    <path d="M272 415 C335 411 407 432 462 446" stroke="#FFF2A0" strokeWidth="13" strokeLinecap="round" opacity=".22" filter={paint('feather')} />
                    <ellipse cx="551" cy="405" rx="31" ry="48" fill={paint('light')} opacity=".5" transform="rotate(-21 551 405)" />
                </g>
            </g>
        </svg>
    );
}
