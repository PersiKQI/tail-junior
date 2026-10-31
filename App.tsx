import React, { useCallback, useEffect, useRef, useState } from 'react';
import { AccessibilityInfo, ActivityIndicator, AppState as NativeAppState, BackHandler, Keyboard, KeyboardAvoidingView, Platform, Pressable, StyleSheet, View } from 'react-native';
import { SafeAreaProvider, useSafeAreaInsets } from 'react-native-safe-area-context';
import { StatusBar } from 'expo-status-bar';
import { useFonts, Manrope_400Regular, Manrope_500Medium, Manrope_700Bold, Manrope_800ExtraBold } from '@expo-google-fonts/manrope';
import type { AppState } from './shared/types';
import { api, ApiError } from './src/api';
import { BusyContext, CharacterContext, Brand, Button, colors, Icon, IconButton, s, Txt, type IconName } from './src/ui';
import { Onboarding } from './src/Onboarding';
import { Home } from './src/Home';
import { Practice } from './src/Practice';
import { Exercise, type ExerciseRoute } from './src/Exercise';
import { CheckinScreen, GoalEditor, GoalScreen } from './src/Goal';
import { DeleteScreen, PrivacyScreen, ProfileScreen } from './src/Profile';
import { BudgetScreen } from './src/Budget';
import { LabScreen } from './src/Lab';
import { ActionsScreen } from './src/Actions';
import { Investments } from './src/Investments';
import { CharacterPicker } from './src/CharacterPicker';
import { IslandNav } from './src/IslandNav';
import { SimulationScreen } from './src/Simulation';

type Tab = 'home' | 'practice' | 'investments' | 'money';
type Screen = null | 'editor' | 'checkin' | 'character' | 'profile' | 'privacy' | 'delete' | 'lab' | 'actions' | ExerciseRoute;

function Application() {
 const [state,setInternalState] = useState<AppState|null>(null);
 const revision=useRef(0);
 const setState=useCallback((value:AppState|null)=>{revision.current++;setInternalState(value);},[]);
 const [error,setError]=useState<string|null>(null),[sessionLost,setSessionLost]=useState(false),[loading,setLoading]=useState(true);
 const [tab,setTab]=useState<Tab>('home'),[screen,setScreen]=useState<Screen>(null),[moneySection,setMoneySection]=useState<'goal'|'budget'>('goal');
 const [characterOrigin,setCharacterOrigin]=useState<'home'|'profile'>('home');
 const [transparencyOff,setTransparencyOff]=useState(false);
 const [investmentEntry,setInvestmentEntry]=useState<string|undefined>();
 const [pending,setPending]=useState(false),[checkinPreset,setCheckinPreset]=useState<number|undefined>();
 const [motionOff,setMotionOff]=useState(false),[toast,setToast]=useState<string|null>(null),[keyboardVisible,setKeyboardVisible]=useState(false);
 const insets=useSafeAreaInsets();
 const boot=useCallback(async()=>{setLoading(true);setError(null);setSessionLost(false);try{setState(await api.bootstrap());}catch(e){setError((e as Error).message);setSessionLost(e instanceof ApiError&&['UNAUTHORIZED','SESSION_EXPIRED','INVALID_SESSION'].includes(e.code));}finally{setLoading(false);}},[setState]);
 useEffect(()=>{void boot();AccessibilityInfo.isReduceMotionEnabled().then(setMotionOff);const sub=AccessibilityInfo.addEventListener('reduceMotionChanged',setMotionOff);return()=>sub.remove();},[boot]);
 useEffect(()=>{if(Platform.OS!=='ios')return;AccessibilityInfo.isReduceTransparencyEnabled().then(setTransparencyOff);const sub=AccessibilityInfo.addEventListener('reduceTransparencyChanged',setTransparencyOff);return()=>sub.remove();},[]);
 useEffect(()=>{if(!toast)return;const t=setTimeout(()=>setToast(null),3200);return()=>clearTimeout(t);},[toast]);
 useEffect(()=>{const show=Keyboard.addListener(Platform.OS==='ios'?'keyboardWillShow':'keyboardDidShow',()=>setKeyboardVisible(true));const hide=Keyboard.addListener(Platform.OS==='ios'?'keyboardWillHide':'keyboardDidHide',()=>setKeyboardVisible(false));return()=>{show.remove();hide.remove();};},[]);
 useEffect(()=>{const sub=BackHandler.addEventListener('hardwareBackPress',()=>{if(pending)return true;if(screen){setScreen(screen==='privacy'||screen==='delete'||(screen==='character'&&characterOrigin==='profile')?'profile':null);return true;}if(tab!=='home'){setTab('home');return true;}return false;});return()=>sub.remove();},[screen,tab,pending,characterOrigin]);
 useEffect(()=>{const sub=NativeAppState.addEventListener('change',next=>{if(next==='active'&&state?.profile.onboardingCompleted){const version=revision.current;void api.refresh().then(value=>{if(version===revision.current)setState(value);}).catch(()=>{});}});return()=>sub.remove();},[state?.profile.onboardingCompleted,setState]);
 function navigate(next:Tab){if(pending)return;Keyboard.dismiss();setInvestmentEntry(undefined);setTab(next);setScreen(null);}
 function openMoney(section:'goal'|'budget'){if(pending)return;setMoneySection(section);navigate('money');}
 function next(current:AppState){const sim=current.simulations.find(x=>x.unlocked&&!x.completed),lesson=current.lessons.find(x=>x.unlocked&&!x.completed);if(sim)setScreen({kind:'simulation',id:sim.id});else if(lesson)setScreen({kind:'lesson',id:lesson.id});else navigate('practice');}
 function openCharacter(origin:'home'|'profile'){if(pending)return;setCharacterOrigin(origin);setScreen('character');}
 function createGoal(){if(pending)return;setMoneySection('goal');setTab('money');setScreen('editor');}
 function openCheckin(amount?:number){if(pending)return;if(!state?.goal){setMoneySection('goal');setTab('money');setScreen('editor');return;}setCheckinPreset(amount);setScreen('checkin');}
 function saved(updated:AppState,message:string){Keyboard.dismiss();setState(updated);setScreen(null);setToast(message);}
 const motion=!motionOff&&!state?.profile.reducedMotion;
 const ready=!!state?.profile.onboardingCompleted&&!loading&&!error;
 const hasTabs=ready&&!screen&&!!state?.profile.character;
 let content:React.ReactNode;
 if(loading)content=<View style={styles.center}><Brand/><ActivityIndicator size="large" color={colors.blue}/><Txt color={colors.muted}>Открываем ТЭЙЛ…</Txt></View>;
 else if(error||!state)content=<View style={[s.page,{flex:1,justifyContent:'center'}]}><Icon name="refresh" color={colors.blue} size={38}/><Txt size={28} weight="extra">{sessionLost?'Профиль недоступен':'Нет связи с ТЭЙЛ'}</Txt><Txt color={colors.muted}>{sessionLost?'Ключ профиля больше не действует. Повтори запрос или начни с новым профилем.':error||'Проверь подключение и попробуй снова.'}</Txt><Button label="Попробовать снова" onPress={boot}/>{sessionLost&&<Button label="Создать новый профиль" variant="secondary" onPress={async()=>{await api.resetSession();await boot();}}/>}</View>;
 else if(!state.profile.onboardingCompleted)content=<Onboarding motion={motion} onBusyChange={setPending} onDone={setState}/>;
 else if(!state.profile.character||screen==='character')content=<CharacterPicker initial={state.profile.character} motion={motion} onBusyChange={setPending} onClose={state.profile.character?()=>setScreen(characterOrigin==='profile'?'profile':null):undefined} onSelect={async character=>{setState(await api.profile({character}));setScreen(characterOrigin==='profile'?'profile':null);}}/>;
 else if(screen==='editor')content=<GoalEditor onBusyChange={setPending} state={state} onSave={v=>saved(v,'Цель сохранена')} onClose={()=>setScreen(null)}/>;
 else if(screen==='checkin')content=<CheckinScreen initialAmount={checkinPreset} onBusyChange={setPending} onSave={v=>saved(v,'Запись сохранена')} onClose={()=>setScreen(null)}/>;
 else if(screen==='lab')content=<LabScreen onCreateGoal={createGoal} state={state} motion={motion} onBusyChange={setPending} onSave={v=>saved(v,'План обновлён')} onClose={()=>setScreen(null)}/>;
 else if(screen==='actions')content=<ActionsScreen onCreateGoal={createGoal} state={state} onState={setState} onBusyChange={setPending} onClose={()=>setScreen(null)}/>;
 else if(screen==='profile')content=<View style={{flex:1}}><View style={styles.detailHeader}><IconButton name="back" label="Вернуться из профиля" onPress={()=>setScreen(null)}/><Txt size={18} weight="bold">Профиль</Txt></View><ProfileScreen onCharacter={()=>openCharacter('profile')} state={state} motion={motion} onState={setState} onBusyChange={setPending} onPrivacy={()=>setScreen('privacy')} onDelete={()=>setScreen('delete')}/></View>;
 else if(screen==='privacy')content=<PrivacyScreen onClose={()=>setScreen('profile')}/>;
 else if(screen==='delete')content=<DeleteScreen onClose={()=>setScreen('profile')} onDeleted={()=>{setState(null);setScreen(null);setTab('home');void boot();}}/>;
 else if(screen?.kind==='simulation')content=<SimulationScreen key={screen.id} id={screen.id} state={state} motion={motion} onState={setState} onBusyChange={setPending} onClose={()=>navigate('practice')} onContinue={next}/>;
 else if(screen)content=<Exercise onBusyChange={setPending} key={`${screen.kind}-${screen.id}`} route={screen} state={state} motion={motion} onState={setState} onClose={()=>navigate('practice')} onContinue={v=>screen.kind==='assessment'?setScreen('profile'):next(v)}/>;
 else if(tab==='home')content=<Home onCharacter={()=>openCharacter('home')} onInvestments={id=>{navigate('investments');setInvestmentEntry(id);}} state={state} motion={motion} onState={setState} onBusyChange={setPending} onPractice={()=>navigate('practice')} onNext={()=>next(state)} onGoal={()=>openMoney('goal')} onProfile={()=>setScreen('profile')} onAssessment={()=>setScreen({kind:'assessment',id:'d7'})} onBudget={()=>openMoney('budget')} onLab={()=>setScreen('lab')} onActions={()=>setScreen('actions')} onCheckin={openCheckin}/>;
 else if(tab==='practice')content=<Practice onInvestments={()=>navigate('investments')} state={state} motion={motion} onLab={()=>setScreen('lab')} onLesson={id=>setScreen({kind:'lesson',id})} onSimulation={id=>setScreen({kind:'simulation',id})} onAssessment={()=>setScreen({kind:'assessment',id:'d7'})} onGoal={()=>openMoney('goal')}/>;
 else if(tab==='investments')content=<Investments key={investmentEntry||'overview'} initialLessonId={investmentEntry} state={state} motion={motion} onState={setState} onBusyChange={setPending} onGoal={()=>openMoney('goal')}/>;
 else content=<View style={{flex:1}}><View style={styles.moneyHeader}><Txt size={27} weight="extra">Мои деньги</Txt><View accessibilityRole="tablist" accessibilityLabel="Разделы личных денег" style={styles.segments}>{([{id:'goal',label:'Цель'},{id:'budget',label:'Бюджет'}] as const).map(item=><Pressable key={item.id} disabled={pending} accessibilityRole="tab" accessibilityLabel={`Мои деньги: ${item.label}`} accessibilityState={{selected:moneySection===item.id,disabled:pending}} aria-selected={moneySection===item.id} onPress={()=>setMoneySection(item.id)} style={[styles.segment,moneySection===item.id&&{backgroundColor:'white'}]}><Txt size={14} weight="bold" color={moneySection===item.id?colors.ink:colors.muted}>{item.label}</Txt></Pressable>)}</View></View>{moneySection==='goal'?<GoalScreen state={state} motion={motion} onState={setState} onBusyChange={setPending} onLab={()=>setScreen('lab')} onActions={()=>setScreen('actions')} onBudget={()=>openMoney('budget')} onEdit={()=>setScreen('editor')} onCheckin={openCheckin} onPractice={()=>navigate('practice')}/>:<BudgetScreen state={state} onGoal={()=>openMoney('goal')} onActions={()=>setScreen('actions')} onState={setState} onLab={()=>setScreen('lab')} onBusyChange={setPending}/>}</View>;
 return <View style={styles.outer}><View style={[styles.app,{paddingTop:insets.top}]}><StatusBar style="dark"/><CharacterContext.Provider value={state?.profile.character||'lumi'}><BusyContext.Provider value={pending}>
  {hasTabs&&<View style={styles.header}><Brand/><Pressable disabled={pending} accessibilityRole="button" accessibilityLabel="Открыть профиль" onPress={()=>setScreen('profile')} style={styles.profile}><Icon name="user" size={23} color={colors.ink}/></Pressable></View>}
  <KeyboardAvoidingView behavior={Platform.OS==='ios'?'padding':undefined} keyboardVerticalOffset={insets.top+(hasTabs?60:0)} style={{flex:1}}><View pointerEvents={pending?'none':'auto'} style={{flex:1}}>{content}</View></KeyboardAvoidingView>
  {hasTabs&&!keyboardVisible?<IslandNav value={tab} onChange={navigate} disabled={pending} bottom={Math.max(insets.bottom,12)} reducedTransparency={transparencyOff}/>:<View style={{height:keyboardVisible?0:insets.bottom}}/>}
  </BusyContext.Provider></CharacterContext.Provider>{toast&&<View accessibilityRole="alert" pointerEvents="none" style={[styles.toast,{bottom:hasTabs?84+insets.bottom:20+insets.bottom}]}><Icon name="check" size={18} color="white"/><Txt color="white" size={14} weight="bold">{toast}</Txt></View>}</View></View>;
}
export default function App(){const[loaded,fontError]=useFonts({Manrope_400Regular,Manrope_500Medium,Manrope_700Bold,Manrope_800ExtraBold});if(!loaded&&!fontError)return <View style={styles.center}><ActivityIndicator color={colors.blue}/></View>;return <SafeAreaProvider><Application/></SafeAreaProvider>;}
const styles=StyleSheet.create({
 outer:{flex:1,backgroundColor:Platform.OS==='web'?'#E8EDF6':colors.bg,alignItems:'center'},app:{flex:1,width:'100%',maxWidth:520,backgroundColor:colors.bg},
 center:{flex:1,alignItems:'center',justifyContent:'center',gap:24,backgroundColor:colors.bg},header:{height:60,paddingHorizontal:20,flexDirection:'row',justifyContent:'space-between',alignItems:'center',backgroundColor:colors.bg},profile:{width:44,height:44,borderRadius:22,backgroundColor:'white',alignItems:'center',justifyContent:'center'},
 detailHeader:{flexDirection:'row',alignItems:'center',gap:12,paddingHorizontal:20,paddingVertical:8},moneyHeader:{paddingHorizontal:20,paddingTop:8,paddingBottom:12,gap:18},segments:{flexDirection:'row',backgroundColor:'#E8EDF5',padding:4,borderRadius:16,gap:4},segment:{flex:1,minHeight:44,borderRadius:12,alignItems:'center',justifyContent:'center'},
 tabs:{flexDirection:'row',backgroundColor:'white',paddingHorizontal:8,paddingTop:6,borderTopWidth:1,borderTopColor:colors.line},tab:{flex:1,minHeight:58,alignItems:'center',justifyContent:'center',gap:2},tabIcon:{width:46,height:32,borderRadius:12,alignItems:'center',justifyContent:'center'},
 toast:{position:'absolute',left:20,right:20,backgroundColor:colors.ink,borderRadius:16,padding:16,flexDirection:'row',gap:10,alignItems:'center'},
});
