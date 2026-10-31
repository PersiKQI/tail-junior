import React, { useState } from 'react';
import { ScrollView, useWindowDimensions, View } from 'react-native';
import type { AppState } from '../shared/types';
import { api } from './api';
import { CharacterPicker } from './CharacterPicker';
import { Brand, Button, Chip, colors, Gradient, Mascot, Txt } from './ui';

export function Onboarding({onDone,motion,onBusyChange}:{onDone:(state:AppState)=>void;motion:boolean;onBusyChange?:(busy:boolean)=>void}) {
  const [choosing,setChoosing]=useState(false);
  const compact=useWindowDimensions().height<760;
  if(choosing)return <CharacterPicker motion={motion} onClose={()=>setChoosing(false)} onBusyChange={onBusyChange} onSelect={async character=>{onDone(await api.profile({character,onboardingCompleted:true}));}}/>;

  return <ScrollView style={{flex:1,backgroundColor:'white'}} showsVerticalScrollIndicator={false} contentContainerStyle={{flexGrow:1}}>
    <Gradient style={{padding:24,paddingBottom:36}}>
      <Brand white/>
      <View style={{minHeight:compact?216:264,alignItems:'center',justifyContent:'center'}}>
        <Mascot size={compact?180:214} animated={motion}/>
        <View style={{position:'absolute',right:0,bottom:16,transform:[{rotate:'5deg'}]}}><Chip label="В твоём темпе" color="white" textColor={colors.deepBlue}/></View>
      </View>
    </Gradient>
    <View style={{flex:1,backgroundColor:'white',marginTop:-24,borderTopLeftRadius:32,borderTopRightRadius:32,padding:24,paddingTop:28,paddingBottom:28,gap:20}}>
      <View style={{gap:12}}>
        <Txt accessibilityRole="header" size={34} weight="extra" style={{letterSpacing:-.9,lineHeight:41}}>Разберись{ '\n'}с деньгами.</Txt>
        <Txt size={16} color={colors.muted} style={{lineHeight:25}}>Накопления, бюджет и первые инвестиционные решения — на учебных примерах. Начни с короткой ситуации.</Txt>
      </View>
      <View style={{gap:14}}>

        <Button label="Выбрать персонажа" onPress={()=>setChoosing(true)}/>
        <Txt color={colors.muted} size={13} style={{textAlign:'center',lineHeight:20}}>Без карты и переводов.{ '\n'}Свою цель можно добавить позже.</Txt>
      </View>
    </View>
  </ScrollView>;
}
